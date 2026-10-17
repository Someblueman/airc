package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/version"
	"github.com/Someblueman/airc/pkg/irc"
)

type doctorReport struct {
	Type          string                 `json:"type"`
	ClientVersion string                 `json:"client_version"`
	Address       string                 `json:"address"`
	Connected     bool                   `json:"connected"`
	Ephemeral     bool                   `json:"ephemeral"`
	Features      map[string]string      `json:"features,omitempty"`
	Server        *protocol.ServerStatus `json:"server,omitempty"`
	Process       processResources       `json:"process"`
	AgentProcess  *processResources      `json:"agent_process,omitempty"`
	Cursor        *cursorHealth          `json:"cursor,omitempty"`
	// Carriers are the fallback carriers tried when native IRC is
	// unreachable; Fallback says which one answered when it was.
	Carriers []string `json:"carriers,omitempty"`
	Fallback string   `json:"fallback,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func runDoctor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	pid := fs.Int("pid", 0, "also count descriptors in this agent/launcher PID; its limit is not inferred")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *pid < 0 {
		return errors.New("usage: airc doctor [--nick NAME] [--pid PID] [--json]")
	}
	ctx, cancel := commandContext()
	defer cancel()
	config := clientConfig(*opt)
	if usesTLS(*opt) {
		config.Network = "tls"
	}
	report := doctorReport{Type: "doctor", ClientVersion: version.String(), Address: config.Network + "://" + config.Addr, Process: inspectResources(ctx, os.Getpid())}
	if *pid > 0 {
		resources := inspectResources(ctx, *pid)
		report.AgentProcess = &resources
	}
	if opt.identityFile != "" {
		if err := identity(opt); err != nil {
			return err
		}
	}
	identity := opt.nick
	if identity == "" {
		identity = os.Getenv("AIRC_NICK")
	}
	if identity != "" {
		report.Cursor = inspectCursor(*opt, identity)
	}
	query := *opt
	if query.identityFile == "" {
		query.nick = defaultQueryNick()
	}
	client, err := dialPrimary(ctx, query)
	if err != nil {
		report.Error = explain(err)
		if fb, ok := fallbackFor(query, err); ok {
			if _, answered, err := fb.Carriers(ctx); err != nil {
				report.Fallback = "no carrier answered: " + err.Error()
			} else {
				report.Fallback = "reachable over " + answered.String()
			}
		}
	} else {
		defer client.Close()
		defer closeOnCancel(ctx, client)()
		report.Connected, report.Ephemeral, report.Features = true, client.Ephemeral(), client.Features()
		for _, feature := range []string{"CONTEXT", "CHAT", "CUSTOM_REACTIONS", "MENTIONS", "DM_AUDIT", "REPLIES", "REACTIONS", "DIRECTORY", "SEARCH", "TOPIC", "CHANNELS", "HISTORY_START"} {
			if !client.Supports(feature) {
				report.Warnings = append(report.Warnings, "Daemon lacks "+feature+"; upgrade/restart it when active work is finished")
			}
		}
		if client.Supports("STATUS") {
			report.Server, err = fetchStatus(ctx, client)
			if err != nil {
				report.Error = err.Error()
			} else {
				if report.Server.Version != report.ClientVersion && report.Server.Version != "devel" && report.ClientVersion != "devel" {
					report.Warnings = append(report.Warnings, "Client and daemon builds differ; replacing a binary does not update the running daemon")
				}
				if report.Server.HistoryLimit == 0 {
					report.Warnings = append(report.Warnings, "History is disabled; offline agents cannot retrieve channel messages")
				}
				if report.Server.PersistenceError != "" {
					report.Warnings = append(report.Warnings, "History persistence failed; messages are only in memory")
				}
			}
		} else {
			report.Warnings = append(report.Warnings, "Daemon version and retention are unknown; this daemon predates STATUS")
		}
	}
	if endpoints, err := fallbackEndpoints(query); err != nil {
		report.Warnings = append(report.Warnings, "Invalid --carriers: "+err.Error())
	} else {
		for _, e := range endpoints {
			report.Carriers = append(report.Carriers, e.String())
		}
	}
	if report.Cursor != nil && report.Cursor.Locked {
		report.Warnings = append(report.Warnings, "Another check holds this identity's cursor lock; finish or cancel that check before starting another")
	}
	if machine := opt.json; machine {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(stdout, "Client: %s\nServer: %s (connected: %t)\n", report.ClientVersion, report.Address, report.Connected)
		if report.Server != nil {
			fmt.Fprintf(stdout, "Daemon: %s, PID %d; clients %d/%d; retained messages %d", report.Server.Version, report.Server.PID, report.Server.Connections, report.Server.MaxConnections, report.Server.HistorySize)
			if report.Server.HistoryMaxBytes > 0 {
				fmt.Fprintf(stdout, " (%d of %d MiB in memory)", report.Server.HistoryBytes>>20, report.Server.HistoryMaxBytes>>20)
			}
			fmt.Fprintf(stdout, "; history file: %t", report.Server.HistoryFile)
			if report.Server.ArchiveBytes > 0 {
				fmt.Fprintf(stdout, " (%d MiB on disk)", report.Server.ArchiveBytes>>20)
			}
			fmt.Fprintln(stdout)
		}
		var features []string
		for key, value := range report.Features {
			features = append(features, key+"="+value)
		}
		sort.Strings(features)
		fmt.Fprintln(stdout, "Features:", strings.Join(features, " "))
		if len(report.Carriers) > 0 {
			fmt.Fprintln(stdout, "Fallback carriers:", strings.Join(report.Carriers, ","))
		}
		if report.Fallback != "" {
			fmt.Fprintln(stdout, "Fallback:", report.Fallback)
		}
		printResources(stdout, report.Process, "CLI")
		if report.AgentProcess != nil {
			printResources(stdout, *report.AgentProcess, "Agent")
		}
		if report.Cursor != nil {
			fmt.Fprintf(stdout, "Cursor: %s (locked: %t)\n", report.Cursor.Path, report.Cursor.Locked)
			if report.Cursor.Owner != nil {
				fmt.Fprintf(stdout, "Lock owner: PID %d since %s\n", report.Cursor.Owner.PID, report.Cursor.Owner.Since)
			}
			if report.Cursor.Error != "" {
				fmt.Fprintln(stdout, "Cursor error:", report.Cursor.Error)
			}
		}
		for _, warning := range report.Warnings {
			fmt.Fprintln(stdout, "Warning:", warning)
		}
	}
	if report.Error != "" {
		return errors.New(report.Error)
	}
	return nil
}

func fetchStatus(ctx context.Context, client *irc.Client) (*protocol.ServerStatus, error) {
	if err := client.Raw("STATUS"); err != nil {
		return nil, err
	}
	var status *protocol.ServerStatus
	err := awaitReply(ctx, client, "server disconnected while reading status", func(event irc.Event) (bool, error) {
		raw, ok := event.(*irc.RawEvent)
		if !ok || raw.Command != "770" {
			return false, nil
		}
		status = new(protocol.ServerStatus)
		if err := json.Unmarshal([]byte(raw.Trailing), status); err != nil {
			return true, fmt.Errorf("decode server status: %w", err)
		}
		return true, nil
	})
	if err != nil {
		return nil, err
	}
	return status, nil
}

func printResources(out io.Writer, r processResources, label string) {
	if r.OpenFiles != nil {
		fmt.Fprintf(out, "%s PID %d: %d open descriptors", label, r.PID, *r.OpenFiles)
	} else {
		fmt.Fprintf(out, "%s PID %d: descriptor count unavailable", label, r.PID)
	}
	if r.SoftLimit != nil {
		fmt.Fprintf(out, "; soft/hard limit %d/%d\n", *r.SoftLimit, *r.HardLimit)
	} else {
		fmt.Fprintln(out, "; limit unknown (inspect/configure the launcher before starting the agent)")
	}
}
