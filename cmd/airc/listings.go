package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func runAgents(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc agents", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	// ACTIVE adds one-shot agents active in the last ten minutes.
	if err := client.Raw("AGENTS ACTIVE"); err != nil {
		return err
	}
	agents := make([]irc.AgentInfo, 0)
	return awaitEvent(ctx, client, 10*time.Second, "listing agents", "timed out waiting for agent list", func(event irc.Event) (bool, error) {
		switch value := event.(type) {
		case *irc.AgentsEvent:
			agents = append(agents, value.Agent)
		case *irc.EndOfAgentsEvent:
			if opt.json {
				return true, json.NewEncoder(stdout).Encode(agents)
			}
			for _, agent := range agents {
				when := agent.ConnectedAt.Format(time.RFC3339)
				if agent.ConnectedAt.IsZero() {
					when = "active " + agent.ActiveAt.Format(time.RFC3339)
				}
				if s := agent.Status; s != nil {
					when += "\t[" + s.State + "] " + s.Task
				}
				if _, err := fmt.Fprintf(stdout, "%s\t%s\t%s\n", agent.Nick, strings.Join(agent.Channels, ","), when); err != nil {
					return true, err
				}
			}
			return true, nil
		}
		return false, nil
	})
}

func runNames(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return errors.New("usage: airc names #channel [--nick NAME] [--json]")
	}
	channel := channelName(args[0])
	fs := flag.NewFlagSet("airc names", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc names #channel [--nick NAME] [--json]")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	ctx, cancel := commandContext()
	defer cancel()
	client, err := dialOneShot(ctx, *opt)
	if err != nil {
		return err
	}
	defer client.Close()
	defer closeOnCancel(ctx, client)()
	if err := client.Names(channel); err != nil {
		return err
	}

	nicks := make([]string, 0)
	return awaitEvent(ctx, client, 10*time.Second, "listing channel members", "timed out waiting for channel members", func(event irc.Event) (bool, error) {
		response, ok := event.(*irc.RawEvent)
		if !ok {
			return false, nil
		}
		switch response.Command {
		case "353":
			if len(response.Params) >= 3 && response.Params[2] == channel {
				nicks = append(nicks, strings.Fields(response.Trailing)...)
			}
		case "366":
			if len(response.Params) < 2 || response.Params[1] != channel {
				return false, nil
			}
			if opt.json {
				return true, json.NewEncoder(stdout).Encode(nicks)
			}
			if len(nicks) == 0 {
				_, err := fmt.Fprintf(stdout, "No members in %s\n", channel)
				return true, err
			}
			_, err := fmt.Fprintf(stdout, "Members of %s: %s\n", channel, strings.Join(nicks, " "))
			return true, err
		case "403", "407", "421", "461":
			return true, fmt.Errorf("cannot list members of %s: %s", channel, response.Trailing)
		}
		return false, nil
	})
}
