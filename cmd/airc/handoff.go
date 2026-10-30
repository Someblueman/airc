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

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// runHandoffSession offers a handoff, or with a step word and an ID takes
// one through accept, blocked, decline, done or withdraw.
func runHandoffSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := handoffInput{}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		in.Step, args = args[0], args[1:]
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			in.ID, args = args[0], args[1:]
		}
	}
	fs := flag.NewFlagSet("airc handoff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	fs.StringVar(&in.Channel, "channel", "", "room to hand the work to, or to post the handoff in (env AIRC_CHANNEL)")
	fs.StringVar(&in.To, "to", "", "nickname the work is for; alone, a direct handoff")
	fs.StringVar(&in.Message, "message", "", "what to hand over, or a note on the step (blocked: why, required); - reads stdin")
	fs.StringVar(&in.Link, "link", "", "done: where the result is, an absolute http(s) URL (a PR, a commit, a document)")
	runFlag := addRunFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	var err error
	if in.Message, err = flagText(in.Message, stdin); err != nil {
		return err
	}
	in.Run = *runFlag
	r, err := in.request()
	if err != nil {
		return err
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
// rather than base64 as the wire carries it. A blocked handoff carries why
// and since when; a done one, the link to its result when one was given.
type handoffRow struct {
	Type          string    `json:"type"`
	ID            string    `json:"id"`
	Target        string    `json:"target"`
	From          string    `json:"from"`
	For           string    `json:"for,omitempty"`
	State         string    `json:"state"`
	Holder        string    `json:"holder,omitempty"`
	BlockedReason string    `json:"blocked_reason,omitempty"`
	BlockedAt     time.Time `json:"blocked_at,omitzero"`
	Link          string    `json:"link,omitempty"`
	Message       string    `json:"message"`
	Timestamp     time.Time `json:"timestamp"`
	Run           string    `json:"run,omitempty"`
}

// handoffHeld reports a state in which the handoff names its holder: the
// holder of a declined or withdrawn handoff no longer holds it.
func handoffHeld(state string) bool {
	return state == "accepted" || state == "blocked" || state == "done"
}

func newHandoffRow(e irc.ChatEntry) handoffRow {
	row := handoffRow{Type: "handoff", ID: e.ID, Target: e.Target, From: e.From, State: e.State, Link: e.Link}
	if handoffHeld(e.State) {
		row.Holder = e.Holder
	}
	if e.State == "blocked" {
		row.BlockedReason, row.BlockedAt = e.BlockedReason, e.BlockedAt
	}
	if e.Message != nil {
		row.For, row.Message, row.Timestamp, row.Run = e.Message.For, e.Message.Message, e.Message.Timestamp, e.Message.Run
	}
	return row
}

// entryState is the state of a handoff or decision entry, as its own listing
// words it.
func entryState(e irc.ChatEntry) string {
	if e.Action == "decision" {
		return decisionState(e)
	}
	return handoffState(e)
}

// handoffState is a handoff's state, with its holder while it has one: the
// holder of a declined or withdrawn handoff no longer holds it.
func handoffState(e irc.ChatEntry) string {
	if e.Holder != "" && handoffHeld(e.State) {
		return e.State + " by " + e.Holder
	}
	return e.State
}

// handoffText is one handoff as a line, its offer indented beneath, then
// why it is blocked or where its result is:
//
//	handoff ID blocked by builder in #work from planner for builder
//	  finish task 7
//	  blocked: waiting on CI
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
	if e.State == "blocked" && e.BlockedReason != "" {
		text.WriteString("\n  blocked: " + indentContinuation(e.BlockedReason))
	}
	if e.Link != "" {
		text.WriteString("\n  done: " + e.Link)
	}
	return text.String()
}
