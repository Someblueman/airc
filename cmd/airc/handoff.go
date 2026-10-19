package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// handoffSteps maps airc handoff's step words to their CHAT actions.
var handoffSteps = map[string]string{
	"accept":   "handoff-accept",
	"decline":  "handoff-decline",
	"done":     "handoff-done",
	"withdraw": "handoff-withdraw",
}

// runHandoffSession offers a handoff, or with a step word and an ID takes
// one through accept, decline, done or withdraw.
func runHandoffSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	step := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		step, args = args[0], args[1:]
		if handoffSteps[step] == "" {
			return fmt.Errorf("unknown handoff step %q: use accept, decline, done or withdraw ID", step)
		}
	}
	id := ""
	if step != "" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc handoff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "room to hand the work to, or to post the handoff in (env AIRC_CHANNEL)")
	to := fs.String("to", "", "nickname the work is for; alone, a direct handoff")
	message := fs.String("message", "", "what to hand over, or a note on the step; - reads stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	r := irc.ChatRequest{Action: "handoff"}
	if step == "" {
		if *channel == "" && *to == "" {
			*channel = os.Getenv("AIRC_CHANNEL")
		}
		if *channel == "" && *to == "" {
			return errors.New("hand off to a room (--channel), a nickname (--to), or a nickname in a room (both)")
		}
		body, err := sendBody("", "", *message, stdin)
		if err != nil {
			return err
		}
		r.Text, r.For = body, *to
		if r.Target = channelName(*channel); r.Target == "" {
			r.Target = *to
		}
	} else {
		if !protocol.ValidMessageID(id) {
			return fmt.Errorf("airc handoff %s needs the handoff's ID", step)
		}
		if *channel != "" || *to != "" {
			return errors.New("a handoff step names only the handoff's ID; it goes where the handoff is")
		}
		if *message != "" {
			body, err := sendBody("", "", *message, stdin)
			if err != nil {
				return err
			}
			r.Text = body
		}
		r.Action, r.ID = handoffSteps[step], id
	}
	if err := identity(opt); err != nil {
		return err
	}
	return handoffRequest(ctx, *opt, r, stdout)
}

// runHandoffsSession lists the handoffs that concern you, a room's, or one.
func runHandoffsSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc handoffs", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "every handoff in this room, finished ones from the last week included")
	id := fs.String("id", "", "one handoff")
	limit := fs.Int("limit", 0, "newest handoffs to list, at most 1000 (default 200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *channel != "" && *id != "" {
		return errors.New("choose --channel or --id")
	}
	if *id != "" && !protocol.ValidMessageID(*id) {
		return errors.New("--id needs a handoff ID")
	}
	r := irc.ChatRequest{Action: "handoffs", Target: channelName(*channel), ID: *id, Limit: *limit}
	if (r.Target != "" || r.ID != "") && opt.nick == "" && opt.identityFile == "" {
		opt.nick = defaultQueryNick()
	} else if err := identity(opt); err != nil {
		return err
	}
	return handoffRequest(ctx, *opt, r, stdout)
}

func handoffRequest(ctx context.Context, opt options, r irc.ChatRequest, stdout io.Writer) error {
	return chatRequestWithContext(ctx, opt, "HANDOFF", func(ctx context.Context, client *irc.Client) error {
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		if !opt.json && len(entries) == 0 && r.Action == "handoffs" {
			_, err := fmt.Fprintln(stdout, "No handoffs.")
			return err
		}
		for _, e := range entries {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(newHandoffRow(e))
			} else {
				_, err = fmt.Fprintln(stdout, handoffText(e))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// handoffRow is a handoff as --json prints it, the offer's text plain
// rather than base64 as the wire carries it.
type handoffRow struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Target    string    `json:"target"`
	From      string    `json:"from"`
	For       string    `json:"for,omitempty"`
	State     string    `json:"state"`
	Holder    string    `json:"holder,omitempty"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func newHandoffRow(e irc.ChatEntry) handoffRow {
	row := handoffRow{Type: "handoff", ID: e.ID, Target: e.Target, From: e.From, State: e.State}
	if e.State == "accepted" || e.State == "done" {
		row.Holder = e.Holder
	}
	if e.Message != nil {
		row.For, row.Message, row.Timestamp = e.Message.For, e.Message.Message, e.Message.Timestamp
	}
	return row
}

// handoffState is a handoff's state, with its holder while it has one: the
// holder of a declined or withdrawn handoff no longer holds it.
func handoffState(e irc.ChatEntry) string {
	if e.Holder != "" && (e.State == "accepted" || e.State == "done") {
		return e.State + " by " + e.Holder
	}
	return e.State
}

// handoffText is one handoff as a line, its offer indented beneath:
//
//	handoff ID accepted by builder in #work from planner for builder
func handoffText(e irc.ChatEntry) string {
	var text strings.Builder
	text.WriteString("handoff " + e.ID + " " + handoffState(e))
	text.WriteString(" in " + e.Target + " from " + e.From)
	if e.Message != nil {
		if e.Message.For != "" && e.Message.For != e.Target {
			text.WriteString(" for " + e.Message.For)
		}
		text.WriteString("\n  " + indentContinuation(e.Message.Message))
	}
	return text.String()
}
