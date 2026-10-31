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

	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// chatRequest runs request over a one-shot connection.
func chatRequest(opt options, request func(context.Context, *irc.Client) error) error {
	ctx, cancel := commandContext()
	defer cancel()
	return chatRequestWithContext(ctx, opt, request)
}

func chatRequestWithContext(ctx context.Context, opt options, request func(context.Context, *irc.Client) error) error {
	client, err := dialOneShot(ctx, opt)
	if err != nil {
		return err
	}
	defer closeOneShot(opt, client)
	defer closeOnCancel(ctx, client)()
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
	human, agent := new(bool), new(bool)
	if kind == "profile" {
		for _, name := range []string{"model", "workspace", "tools", "about"} {
			fields[name] = fs.String(name, "", "self-reported profile "+name+", up to 400 bytes")
		}
		human, agent = addKindFlags(fs, "self-reported kind: ")
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
	if profileKind, err := kindPatch(*human, *agent); err != nil {
		return err
	} else if profileKind != "" {
		patch["kind"] = profileKind
	}
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
	writeCard := func(card irc.AgentCard) error {
		if opt.json {
			return json.NewEncoder(stdout).Encode(&irc.DirectoryEvent{Type: "agent", AgentCard: card})
		}
		_, err := fmt.Fprintln(stdout, cardText(card))
		return err
	}
	if !changing {
		// Reading cards gives the same answer over any carrier; updates
		// stay native (a gateway retry of one could apply it twice).
		return readRacing(ctx, *opt, func(ctx context.Context, client *irc.Client) error {
			if err := client.Directory(*who); err != nil {
				return err
			}
			return awaitReply(ctx, client, "server disconnected reading directory", func(event irc.Event) (bool, error) {
				switch value := event.(type) {
				case *irc.DirectoryEvent:
					return false, writeCard(value.AgentCard)
				case *irc.EndOfDirectoryEvent:
					return true, nil
				}
				return false, nil
			})
		}, func(ctx context.Context, conn *fallback.Conn) (fallback.Endpoint, error) {
			cards, answered, err := conn.Directory(ctx, *who)
			for _, card := range cards {
				if err == nil {
					err = writeCard(card)
				}
			}
			return answered, err
		})
	}
	return chatRequestWithContext(ctx, *opt, func(ctx context.Context, client *irc.Client) error {
		var err error
		if kind == "profile" {
			if *clear {
				patch["clear"] = "true"
			}
			err = client.UpdateProfile(patch)
		} else {
			if *clear {
				*set = "clear"
			}
			err = client.SetPresence(*set, *note, *ttl)
		}
		if err != nil {
			return err
		}
		return awaitReply(ctx, client, "server disconnected reading directory", func(event irc.Event) (bool, error) {
			switch value := event.(type) {
			case *irc.DirectoryEvent:
				return false, writeCard(value.AgentCard)
			case *irc.EndOfDirectoryEvent:
				return true, nil
			}
			return false, nil
		})
	})
}

// addKindFlags adds --human and --agent, which say what a nickname is.
func addKindFlags(fs *flag.FlagSet, prefix string) (human, agent *bool) {
	human = fs.Bool("human", false, prefix+"a person; decisions are meant for humans")
	agent = fs.Bool("agent", false, prefix+"an AI agent")
	return human, agent
}

// kindPatch is the profile kind --human or --agent asks for, "" for neither.
func kindPatch(human, agent bool) (string, error) {
	switch {
	case human && agent:
		return "", errors.New("--human and --agent name different kinds; pass one")
	case human:
		return "human", nil
	case agent:
		return "agent", nil
	}
	return "", nil
}

func cardText(card irc.AgentCard) string {
	parts := []string{card.Nick + ": " + card.State}
	if card.Kind != "" {
		parts = append(parts, card.Kind)
	}
	if card.Connected {
		parts = append(parts, "connected")
	} else if card.Active {
		parts = append(parts, strings.TrimSpace("active "+strings.Join(card.Rooms, ",")))
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
	if s := card.Status; s != nil {
		parts = append(parts, strings.TrimSpace("status: ["+s.State+"] "+s.Task+" in "+s.Target+" "+s.Link))
	}
	return strings.Join(parts, "; ")
}
