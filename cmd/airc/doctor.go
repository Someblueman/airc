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
	Warnings      []string               `json:"warnings,omitempty"`
	Error         string                 `json:"error,omitempty"`
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
	client, err := dialOneShot(ctx, query)
	if err != nil {
		report.Error = explain(err)
	} else {
		defer client.Close()
		stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
		defer stopClose()
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
			fmt.Fprintf(stdout, "Daemon: %s, PID %d; clients %d/%d; retained messages %d/%d; history file: %t\n", report.Server.Version, report.Server.PID, report.Server.Connections, report.Server.MaxConnections, report.Server.HistorySize, report.Server.HistoryLimit, report.Server.HistoryFile)
		}
		var features []string
		for key, value := range report.Features {
			features = append(features, key+"="+value)
		}
		sort.Strings(features)
		fmt.Fprintln(stdout, "Features:", strings.Join(features, " "))
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
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return nil, errors.New("server disconnected while reading status")
			}
			if raw, ok := event.(*irc.RawEvent); ok && raw.Command == "770" {
				var status protocol.ServerStatus
				if err := json.Unmarshal([]byte(raw.Trailing), &status); err != nil {
					return nil, fmt.Errorf("decode server status: %w", err)
				}
				return &status, nil
			}
			if err := serverError(event); err != nil {
				return nil, err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
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
