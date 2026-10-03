package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func chatRequest(opt options, capability string, request func(context.Context, *irc.Client) error) error {
	ctx, cancel := commandContext()
	defer cancel()
	return chatRequestWithContext(ctx, opt, capability, request)
}

func chatRequestWithContext(ctx context.Context, opt options, capability string, request func(context.Context, *irc.Client) error) error {
	client, err := dialOneShot(ctx, opt)
	if err != nil {
		return err
	}
	defer closeOneShot(opt, client)
	defer closeOnCancel(ctx, client)()
	if !client.Supports(capability) {
		return fmt.Errorf("this aircd lacks %s; upgrade when safe", capability)
	}
	return request(ctx, client)
}

func runDirectoryCommandSession(ctx context.Context, session *agentConnection, kind string, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc "+kind, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	who := fs.String("who", "", "read a particular nickname's card")
	clear, set, note, ttl := new(bool), new(string), new(string), new(time.Duration)
	if kind != "directory" {
		fs.BoolVar(clear, "clear", false, "clear your profile or activity state")
	}
	if kind == "presence" {
		fs.StringVar(set, "set", "", "presence state: available/thinking/running/away")
		fs.StringVar(note, "message", "", "presence note, up to 240 bytes")
		fs.DurationVar(ttl, "ttl", 5*time.Minute, "presence expiry, 1s-1h; no background heartbeat")
	}
	fields := map[string]*string{}
	if kind == "profile" {
		for _, name := range []string{"model", "workspace", "tools", "about"} {
			fields[name] = fs.String(name, "", "self-reported profile "+name+", up to 400 bytes")
		}
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	patch := map[string]string{}
	presenceFlags := false
	fs.Visit(func(f *flag.Flag) {
		if value, ok := fields[f.Name]; ok {
			patch[f.Name] = *value
		}
		if f.Name == "set" || f.Name == "message" || f.Name == "ttl" {
			presenceFlags = true
		}
	})
	if fs.NArg() != 0 || *who != "" && (*clear || len(patch) > 0 || presenceFlags) {
		return errors.New("use directory --who NICK, profile fields/--clear, or presence --set STATE/--clear")
	}
	if *clear && (len(patch) > 0 || presenceFlags) {
		return errors.New("--clear cannot be combined with updates")
	}
	if kind == "presence" && presenceFlags && *set == "" {
		return errors.New("presence updates require --set STATE")
	}
	changing := *clear || len(patch) > 0 || *set != ""
	if changing || kind != "directory" && *who == "" {
		if err := identity(opt); err != nil {
			return err
		}
	} else if err := queryIdentity(opt); err != nil {
		return err
	}
	if kind != "directory" && *who == "" {
		*who = opt.nick
	}
	return chatRequestWithContext(ctx, *opt, "DIRECTORY", func(ctx context.Context, client *irc.Client) error {
		var err error
		switch {
		case kind == "profile" && changing:
			if *clear {
				patch["clear"] = "true"
			}
			err = client.UpdateProfile(patch)
		case kind == "presence" && changing:
			if *clear {
				*set = "clear"
			}
			err = client.SetPresence(*set, *note, *ttl)
		default:
			err = client.Directory(*who)
		}
		if err != nil {
			return err
		}
		for {
			select {
			case event, ok := <-client.Events():
				if !ok {
					return errors.New("server disconnected reading directory")
				}
				if err := serverError(event); err != nil {
					return err
				}
				switch value := event.(type) {
				case *irc.DirectoryEvent:
					if opt.json {
						err = json.NewEncoder(stdout).Encode(value)
					} else {
						_, err = fmt.Fprintln(stdout, cardText(value.AgentCard))
					}
					if err != nil {
						return err
					}
				case *irc.EndOfDirectoryEvent:
					return nil
				}
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	})
}

func cardText(card irc.AgentCard) string {
	parts := []string{card.Nick + ": " + card.State}
	if card.Connected {
		parts = append(parts, "connected")
	}
	if !card.LastSeen.IsZero() {
		parts = append(parts, "last seen "+card.LastSeen.Format(time.RFC3339))
	}
	if !card.ExpiresAt.IsZero() {
		parts = append(parts, "expires "+card.ExpiresAt.Format(time.RFC3339))
	}
	for _, field := range []struct{ label, text string }{{"model", card.Model}, {"workspace", card.Workspace}, {"tools", card.Tools}, {"about", card.About}, {"note", card.Note}} {
		if field.text != "" {
			parts = append(parts, field.label+": "+field.text)
		}
	}
	return strings.Join(parts, "; ")
}
