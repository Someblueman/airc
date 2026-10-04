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

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/pkg/irc"
)

func runChatCommandSession(ctx context.Context, session *agentConnection, kind string, args []string, stdout, stderr io.Writer) error {
	positional := []string{}
	for len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional = append(positional, args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("airc "+kind, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "room target")
	text := fs.String("message", "", "message, correction, action or activity note")
	question := fs.String("question", "", "poll question")
	duration := fs.Duration("for", 120*time.Second, "activity/poll expiry")
	eta := fs.Duration("eta", 0, "reply expected within this time (1s-15m)")
	cancel := fs.Bool("cancel", false, "cancel your reply-coming signal")
	wait := fs.Duration("wait", 0, "wait for a textual answer to this question, at most 5m")
	slow := fs.Duration("slow", -time.Second, "admin: room posting delay, 0 disables")
	retention := fs.Int("retention", -1, "admin: room history quota, 0 disables")
	asOperator := fs.Bool("as-operator", false, "room: use your account operator grant instead of the admin credential")
	tokenFile := fs.String("token-file", "", "admin credential for room configuration")
	var choices pollChoices
	fs.Var(&choices, "option", "poll choice (repeat 2-8 times)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *asOperator && (kind != "room" || *tokenFile != "") {
		return errors.New("--as-operator is for room configuration and cannot be combined with --token-file")
	}
	if *eta < 0 {
		return errors.New("--eta must not be negative")
	}
	if kind == "room" && (*slow < 0 && *slow != -time.Second || *slow%time.Second != 0) {
		return errors.New("--slow must be whole seconds (0-3600s), or -1s to read")
	}
	durationSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "for" {
			durationSet = true
		}
	})
	positional = append(positional, fs.Args()...)
	r := irc.ChatRequest{Action: kind, Text: *text, Target: channelName(*channel), Seconds: int64((*duration + time.Second - 1) / time.Second)}
	switch kind {
	case "pin", "unpin", "correct", "retract", "prepare", "waiting", "poll-results", "poll-close":
		if len(positional) != 1 {
			return errors.New("this command requires one message ID")
		}
		r.ID = positional[0]
	case "pins", "room":
		if len(positional) != 1 {
			return errors.New("this command requires one room")
		}
		r.Target = channelName(positional[0])
	case "vote":
		if len(positional) != 2 {
			return errors.New("vote requires POLL_ID CHOICE_NUMBER")
		}
		r.ID = positional[0]
		if _, err := fmt.Sscan(positional[1], &r.Choice); err != nil {
			return err
		}
	case "me", "typing", "thinking", "poll":
		if len(positional) != 0 {
			return errors.New("use --channel and --message (or --question/--option for polls)")
		}
	}
	if kind == "me" {
		r.Action = "action"
	}
	if kind == "poll-results" {
		r.Action = "results"
	}
	if kind == "poll-close" {
		r.Action = "close-poll"
	}
	if kind == "poll" {
		r.Text, r.Options = *question, choices
	}
	if kind == "prepare" {
		if *eta > 0 {
			r.Seconds = int64((*eta + time.Second - 1) / time.Second)
		}
		if *cancel {
			r.Action = "cancel"
		}
	}
	if kind == "typing" || kind == "thinking" {
		if !durationSet {
			r.Seconds = 10
		}
	}
	changingRoom := kind == "room" && (*slow >= 0 || *retention >= 0)
	if kind == "room" {
		r.Seconds, r.Limit = int64(*slow/time.Second), *retention
	}
	if *wait < 0 || *wait > 5*time.Minute || *wait > 0 && kind != "waiting" {
		return errors.New("--wait applies to waiting and must be 0-5m")
	}
	query := kind == "pins" || kind == "room" || kind == "waiting" || kind == "poll-results"
	if query && opt.nick == "" && opt.identityFile == "" {
		opt.nick = defaultQueryNick()
	} else if err := identity(opt); err != nil {
		return err
	}
	if *wait > 0 {
		return waitForAnswer(*opt, r.ID, *wait, stdout, stderr)
	}
	return chatRequestWithContext(ctx, *opt, "CHAT", func(ctx context.Context, client *irc.Client) error {
		if changingRoom && !*asOperator {
			path := *tokenFile
			if path == "" {
				var err error
				path, err = defaultAdminTokenFile()
				if err != nil {
					return err
				}
			}
			token, err := admin.ReadToken(path)
			if err != nil {
				return err
			}
			if err := client.AuthenticateAdmin(token); err != nil {
				return err
			}
		}
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(e)
			} else {
				_, err = fmt.Fprintln(stdout, chatEntryText(e))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func chatEntryText(e irc.ChatEntry) string {
	var text strings.Builder
	text.WriteString(e.Action + " " + e.Target + " " + e.ID)
	if e.From != "" {
		text.WriteString(" by " + e.From)
	}
	if e.Message != nil {
		text.WriteString(": " + indentContinuation(e.Message.Message))
	}
	if e.Text != "" {
		text.WriteString(": " + e.Text)
	}
	if !e.ExpiresAt.IsZero() {
		text.WriteString("; expires " + e.ExpiresAt.Format(time.RFC3339))
	}
	if e.Action == "room" {
		fmt.Fprintf(&text, "; slow=%ds retention=%d", e.SlowSeconds, e.HistoryLimit)
	}
	for i, option := range e.Options {
		count := 0
		if i < len(e.Votes) {
			count = e.Votes[i]
		}
		fmt.Fprintf(&text, "\n  %d. %s (%d)", i+1, option, count)
	}
	if e.Closed {
		text.WriteString("; closed")
	}
	return text.String()
}

// Repeat --option without splitting punctuation inside a choice.
type pollChoices []string

func (p *pollChoices) String() string         { return strings.Join(*p, ", ") }
func (p *pollChoices) Set(value string) error { *p = append(*p, value); return nil }
