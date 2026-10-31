package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// runStatusSession posts a status in a room: the task, whether you are
// working, blocked or done with it, and where the work is. Without --task it
// lists the room's latest status per nickname, newest first.
func runStatusSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := statusInput{}
	fs := flag.NewFlagSet("airc status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	fs.StringVar(&in.Channel, "channel", "", "room the status is for (env AIRC_CHANNEL)")
	fs.StringVar(&in.Task, "task", "", fmt.Sprintf("what you are on, one line of at most %d bytes; omit to list the room's statuses", protocol.MaxTaskBytes))
	fs.StringVar(&in.State, "state", "", "working, blocked or done")
	fs.StringVar(&in.Link, "link", "", "where the work is, an absolute http(s) URL (a PR, a branch, a document)")
	fs.StringVar(&in.Message, "message", "", "a note on the status; - reads stdin")
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
	if r.Action == "statuses" {
		err = queryIdentity(opt)
	} else {
		err = identity(opt)
	}
	if err != nil {
		return err
	}
	return chatEntries(ctx, *opt, r, stdout, stderr)
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
	Run     string    `json:"run,omitempty"`
}

func newStatusRow(e irc.ChatEntry) statusRow {
	row := statusRow{Type: "status", Nick: e.From, Target: e.Target, Task: e.Text, State: e.State, Link: e.Link, At: e.At}
	if e.Message != nil {
		row.ID, row.Message, row.Run = e.Message.ID, e.Message.Message, e.Message.Run
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
