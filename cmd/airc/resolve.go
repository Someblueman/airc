package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// runResolveSession closes a question that is settled: it replies to the
// root of the message's thread with a resolution, so a digest stops waiting
// on it. The root's author, whoever it addresses, or an admin may resolve it.
func runResolveSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := resolveInput{}
	if len(args) > 0 && protocol.ValidMessageID(args[0]) {
		in.ID, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc resolve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	fs.StringVar(&in.Message, "message", "", "how it was settled; - reads stdin (default \"Resolved\")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if in.ID == "" || fs.NArg() > 0 {
		return errors.New("usage: airc resolve MESSAGE_ID --nick N [--message NOTE] [--json]")
	}
	var err error
	if in.Message, err = flagText(in.Message, stdin); err != nil {
		return err
	}
	r, err := in.request()
	if err != nil {
		return err
	}
	if err := identity(opt); err != nil {
		return err
	}
	return chatRequestWithContext(ctx, *opt, "CHAT", func(ctx context.Context, client *irc.Client) error {
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(newResolveRow(e))
			} else {
				_, err = fmt.Fprintln(stdout, resolveText(e))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// resolveRow is a resolution as --json prints it: the root it closed, where,
// by whom, and the resolution message's ID and body.
type resolveRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	From      string    `json:"from"`
	MessageID string    `json:"message_id"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func newResolveRow(e irc.ChatEntry) resolveRow {
	row := resolveRow{Type: "resolved", ID: e.ID, Target: e.Target, From: e.From}
	if e.Message != nil {
		row.MessageID, row.Message, row.Timestamp = e.Message.ID, e.Message.Message, e.Message.Timestamp
	}
	return row
}

// resolveText is one resolution as a line:
//
//	resolved ID in #work by builder: settled in the DM
func resolveText(e irc.ChatEntry) string {
	text := "resolved " + e.ID + " in " + e.Target + " by " + e.From
	if e.Message != nil {
		text += ": " + indentContinuation(e.Message.Message)
	}
	return text
}
