package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/pkg/irc"
)

func runRoomControl(action string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc "+action, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel to administer")
	target := fs.String("who", "", "registered operator or connected kick target")
	reason := fs.String("reason", "", "kick reason")
	tokenFile := fs.String("token-file", "", "optional admin credential, required to grant the first operator")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *channel == "" || action != "operators" && *target == "" {
		return errors.New("use airc op|deop|kick --channel '#room' --who NICK, or airc operators --channel '#room'")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	return chatRequest(*opt, "CHANNEL_OPERATORS", func(ctx context.Context, c *irc.Client) error {
		if *tokenFile != "" {
			token, err := admin.ReadToken(*tokenFile)
			if err != nil {
				return err
			}
			if err := c.AuthenticateAdmin(token); err != nil {
				return err
			}
		}
		room := channelName(*channel)
		var err error
		switch action {
		case "op", "deop":
			err = c.SetOperator(room, *target, action == "op")
		case "operators":
			err = c.Operators(room)
		case "kick":
			err = c.Kick(room, *target, *reason)
		}
		if err != nil {
			return err
		}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event, ok := <-c.Events():
				if !ok {
					return disconnectError("disconnected before room command was confirmed")
				}
				if err := serverError(event); err != nil {
					return err
				}
				done, show := false, false
				switch e := event.(type) {
				case *irc.KickEvent:
					done, show = action == "kick" && e.Channel == room, true
				case *irc.RawEvent:
					show = e.Command == "MODE" || e.Command == "783" || e.Command == "324"
					done = e.Command == "MODE" && action != "operators" || e.Command == "324" && action == "operators"
				}
				if show {
					if opt.json {
						err = json.NewEncoder(stdout).Encode(event)
					} else {
						switch e := event.(type) {
						case *irc.KickEvent:
							_, err = fmt.Fprintf(stdout, "Kicked %s from %s; rejoining is allowed\n", e.Agent, e.Channel)
						case *irc.RawEvent:
							_, err = fmt.Fprintln(stdout, strings.Join(e.Params[1:], " "), e.Trailing)
						}
					}
					if err != nil {
						return err
					}
				}
				if done {
					return nil
				}
			}
		}
	})
}

func runMonitor(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc monitor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	nicks := fs.String("who", "", "comma-separated nicknames to monitor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *nicks == "" {
		return errors.New("use airc monitor --who alice,bob [--json]; runs until interrupted")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	cfg, err := dialConfig(*opt)
	if err != nil {
		return err
	}
	cfg.Ephemeral, cfg.Reconnect = true, true
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	c, err := irc.DialContext(ctx, cfg)
	if err != nil {
		return err
	}
	defer c.Close()
	if !c.Supports("MONITOR") {
		return errors.New("monitor requires an updated daemon")
	}
	if err := c.Monitor(strings.Split(*nicks, ",")...); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-c.Events():
			if !ok {
				return disconnectError("monitor connection closed")
			}
			if err := serverError(event); err != nil {
				return err
			}
			switch e := event.(type) {
			case *irc.MonitorEvent:
				if opt.json {
					err = json.NewEncoder(stdout).Encode(e)
				} else {
					state := "offline"
					if e.Online {
						state = "online"
					}
					_, err = fmt.Fprintln(stdout, strings.Join(e.Nicks, ","), state)
				}
			case *irc.ConnectionEvent:
				if opt.json {
					err = json.NewEncoder(stdout).Encode(e)
				} else {
					_, err = fmt.Fprintf(stdout, "connection: connected=%t %s\n", e.Connected, e.Error)
				}
			}
			if err != nil {
				return err
			}
		}
	}
}
