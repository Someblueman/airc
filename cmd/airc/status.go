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

// runStatusSession posts a status in a room: the task, whether you are
// working, blocked or done with it, and where the work is. Without --task it
// lists the room's latest status per nickname, newest first.
func runStatusSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "room the status is for (env AIRC_CHANNEL)")
	task := fs.String("task", "", fmt.Sprintf("what you are on, one line of at most %d bytes; omit to list the room's statuses", protocol.MaxTaskBytes))
	state := fs.String("state", "", "working, blocked or done")
	link := fs.String("link", "", "where the work is, an absolute http(s) URL (a PR, a branch, a document)")
	message := fs.String("message", "", "a note on the status; - reads stdin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *channel == "" {
		*channel = os.Getenv("AIRC_CHANNEL")
	}
	room := channelName(*channel)
	if !protocol.IsChannel(room) {
		return errors.New("a status is for a room: --channel ROOM")
	}
	r := irc.ChatRequest{Action: "statuses", Target: room}
	if *task == "" {
		if *state != "" || *link != "" || *message != "" {
			return errors.New("--state, --link and --message go with --task; without --task, status lists the room's statuses")
		}
		if opt.nick == "" && opt.identityFile == "" {
			opt.nick = defaultQueryNick()
		} else if err := identity(opt); err != nil {
			return err
		}
		return statusRequest(ctx, *opt, r, stdout)
	}
	if !protocol.ValidStatusState(*state) {
		return errors.New("--state is working, blocked or done")
	}
	if *link != "" && !protocol.ValidLink(*link) {
		return fmt.Errorf("--link needs an absolute http(s) URL of at most %d bytes", protocol.MaxLinkBytes)
	}
	note := ""
	if *message != "" {
		body, err := sendBody("", "", *message, stdin)
		if err != nil {
			return err
		}
		note = body
	}
	r = irc.ChatRequest{Action: "status", Target: room, Task: *task, State: *state, Link: *link, Text: note}
	if err := identity(opt); err != nil {
		return err
	}
	return statusRequest(ctx, *opt, r, stdout)
}

func statusRequest(ctx context.Context, opt options, r irc.ChatRequest, stdout io.Writer) error {
	return chatRequestWithContext(ctx, opt, "CHAT", func(ctx context.Context, client *irc.Client) error {
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		if !opt.json && len(entries) == 0 {
			_, err := fmt.Fprintln(stdout, "No statuses in "+r.Target+".")
			return err
		}
		for _, e := range entries {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(newStatusRow(e))
			} else {
				_, err = fmt.Fprintln(stdout, statusText(e))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// statusRow is a status as --json prints it: whose it is, the task, the
// state, the link, when it was posted and, on a post, the message's ID and
// body.
type statusRow struct {
	Type    string    `json:"type"`
	Nick    string    `json:"nick"`
	Target  string    `json:"target"`
	Task    string    `json:"task"`
	State   string    `json:"state"`
	Link    string    `json:"link,omitempty"`
	At      time.Time `json:"at"`
	ID      string    `json:"id,omitempty"`
	Message string    `json:"message,omitempty"`
}

func newStatusRow(e irc.ChatEntry) statusRow {
	row := statusRow{Type: "status", Nick: e.From, Target: e.Target, Task: e.Text, State: e.State, Link: e.Link, At: e.At}
	if e.Message != nil {
		row.ID, row.Message = e.Message.ID, e.Message.Message
	}
	return row
}

// statusText is one status as a line, the note indented beneath:
//
//	builder [done] port the fix in #work https://github.com/o/r/pull/54
//	  merged in #54
func statusText(e irc.ChatEntry) string {
	var text strings.Builder
	text.WriteString(e.From + " [" + e.State + "] " + e.Text + " in " + e.Target)
	if e.Link != "" {
		text.WriteString(" " + e.Link)
	}
	if e.Message != nil {
		if _, note, ok := strings.Cut(e.Message.Message, protocol.StatusText(e.State, e.Text, "")+": "); ok {
			text.WriteString("\n  " + indentContinuation(note))
		}
	}
	return text.String()
}

// statusMark is a one-character marker of a status state, for a member
// pane or a listing: working », blocked !, done ✓.
func statusMark(state string) string {
	switch state {
	case "working":
		return "»"
	case "blocked":
		return "!"
	case "done":
		return "✓"
	}
	return ""
}
