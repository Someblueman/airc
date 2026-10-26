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

// decisionSteps maps airc decide's step words to their CHAT actions.
var decisionSteps = map[string]string{
	"answer":   "decision-answer",
	"withdraw": "decision-withdraw",
}

// runDecideSession asks a named decider, usually a human, to decide
// something, or with a step word and an ID answers or withdraws a decision.
func runDecideSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	step := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		step, args = args[0], args[1:]
		if decisionSteps[step] == "" {
			return fmt.Errorf("unknown decision step %q: use answer or withdraw ID", step)
		}
	}
	id := ""
	if step != "" && len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		id, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("airc decide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "room to ask in (env AIRC_CHANNEL); needs --from")
	from := fs.String("from", "", "nickname who decides, usually a human; required in a room")
	to := fs.String("to", "", "nickname who decides, as a direct decision")
	message := fs.String("message", "", "what to decide and why it matters, or a note on the step; - reads stdin")
	var options pollChoices
	fs.Var(&options, "option", "a choice to decide between, numbered from 1; repeat for 2-8 options")
	choice := fs.Int("choice", 0, "answer: the chosen option's number")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	r := irc.ChatRequest{Action: "decision"}
	if step == "" {
		if *choice != 0 {
			return errors.New("--choice applies to answer")
		}
		if *channel == "" && *to == "" {
			*channel = os.Getenv("AIRC_CHANNEL")
		}
		if *to != "" && (*channel != "" || *from != "" && !strings.EqualFold(*from, *to)) {
			return errors.New("--to names the decider of a direct decision; use --from in a room")
		}
		if *channel == "" && *to == "" || *channel != "" && *from == "" {
			return errors.New("ask a room with --channel --from, or a nickname with --to")
		}
		body, err := sendBody("", "", *message, stdin)
		if err != nil {
			return err
		}
		r.Text, r.For, r.Options = body, *from, options
		if r.Target = channelName(*channel); r.Target == "" {
			r.Target, r.For = *to, *to
		}
	} else {
		if !protocol.ValidMessageID(id) {
			return fmt.Errorf("airc decide %s needs the decision's ID", step)
		}
		if *channel != "" || *from != "" || *to != "" || len(options) > 0 {
			return errors.New("a decision step names only the decision's ID; it goes where the decision is")
		}
		if *choice != 0 && step != "answer" {
			return errors.New("--choice applies to answer")
		}
		if *choice < 0 {
			return errors.New("--choice numbers an option from 1")
		}
		if *message != "" {
			body, err := sendBody("", "", *message, stdin)
			if err != nil {
				return err
			}
			r.Text = body
		}
		r.Action, r.ID, r.Choice = decisionSteps[step], id, *choice
	}
	if err := identity(opt); err != nil {
		return err
	}
	return decisionRequest(ctx, *opt, r, stdout, stderr)
}

// runDecisionsSession lists the decisions that concern you, a room's, or one.
func runDecisionsSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc decisions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	channel := fs.String("channel", "", "every decision asked in this room, answered ones from the last week included")
	id := fs.String("id", "", "one decision")
	limit := fs.Int("limit", 0, "newest decisions to list, at most 1000 (default 200)")
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
		return errors.New("--id needs a decision ID")
	}
	r := irc.ChatRequest{Action: "decisions", Target: channelName(*channel), ID: *id, Limit: *limit}
	if (r.Target != "" || r.ID != "") && opt.nick == "" && opt.identityFile == "" {
		opt.nick = defaultQueryNick()
	} else if err := identity(opt); err != nil {
		return err
	}
	return decisionRequest(ctx, *opt, r, stdout, stderr)
}

// decisionRequest sends r and prints the decisions answered. Before asking,
// it looks the decider up and warns on stderr when they are not marked as a
// human: a decision is meant for a person, but an agent may decide too.
func decisionRequest(ctx context.Context, opt options, r irc.ChatRequest, stdout, stderr io.Writer) error {
	return chatRequestWithContext(ctx, opt, "DECISION", func(ctx context.Context, client *irc.Client) error {
		if r.Action == "decision" {
			warnDeciderKind(ctx, client, r.For, stderr)
		}
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		if !opt.json && len(entries) == 0 && r.Action == "decisions" {
			_, err := fmt.Fprintln(stdout, "No decisions.")
			return err
		}
		for _, e := range entries {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(newDecisionRow(e))
			} else {
				_, err = fmt.Fprintln(stdout, decisionText(e))
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// warnDeciderKind reads nick's directory card over client and warns when its
// kind is not human. It never refuses: a lookup that fails is one line too.
func warnDeciderKind(ctx context.Context, client *irc.Client, nick string, stderr io.Writer) {
	if !client.Supports("DIRECTORY") {
		return
	}
	kind, err := lookupKind(ctx, client, nick)
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "airc: warning: could not look up %s's card: %v\n", nick, err)
	case kind != "human":
		fmt.Fprintf(stderr, "airc: warning: %s is not registered as a human (kind: %s); posting anyway\n", nick, orUnset(kind))
	}
}

// lookupKind returns nick's profile kind, or "" when it has none.
func lookupKind(ctx context.Context, client *irc.Client, nick string) (string, error) {
	if err := client.Directory(nick); err != nil {
		return "", err
	}
	kind := ""
	err := awaitReply(ctx, client, "connection closed reading the directory", func(event irc.Event) (bool, error) {
		switch v := event.(type) {
		case *irc.DirectoryEvent:
			if strings.EqualFold(v.Nick, nick) {
				kind = v.Kind
			}
		case *irc.EndOfDirectoryEvent:
			return true, nil
		}
		return false, nil
	})
	return kind, err
}

func orUnset(kind string) string {
	if kind == "" {
		return "unset"
	}
	return kind
}

// decisionRow is a decision as --json prints it, the question plain rather
// than base64 as the wire carries it, with the answer's fields once answered.
type decisionRow struct {
	Type            string    `json:"type"`
	ID              string    `json:"id"`
	Target          string    `json:"target"`
	From            string    `json:"from"`
	Decider         string    `json:"decider"`
	State           string    `json:"state"`
	Message         string    `json:"message"`
	Options         []string  `json:"options,omitempty"`
	Timestamp       time.Time `json:"timestamp"`
	Choice          int       `json:"choice,omitempty"`
	Answer          string    `json:"answer,omitempty"`
	AnsweredBy      string    `json:"answered_by,omitempty"`
	AnsweredAccount string    `json:"answered_account,omitempty"`
	AnsweredAt      time.Time `json:"answered_at,omitzero"`
}

func newDecisionRow(e irc.ChatEntry) decisionRow {
	row := decisionRow{Type: "decision", ID: e.ID, Target: e.Target, From: e.From, State: e.State}
	if e.Message != nil {
		row.Decider, row.Message, row.Options, row.Timestamp = e.Message.For, e.Message.Message, e.Message.Choices, e.Message.Timestamp
	}
	if a := e.Answer; a != nil {
		row.Choice, row.Answer, row.AnsweredBy, row.AnsweredAccount, row.AnsweredAt = a.Choice, a.Message, a.From, a.AccountID, a.Timestamp
	}
	return row
}

// decisionState is a decision's state with who it waits on, or who answered
// it and what they chose:
//
//	open for sws
//	answered by sws: 2. no, rewrite it
func decisionState(e irc.ChatEntry) string {
	switch {
	case e.State == "open" && e.Message != nil && e.Message.For != "":
		return "open for " + e.Message.For
	case e.State == "answered" && e.Answer != nil:
		text := "answered by " + e.Answer.From
		if e.Answer.Choice > 0 && e.Message != nil && e.Answer.Choice <= len(e.Message.Choices) {
			text += fmt.Sprintf(": %d. %s", e.Answer.Choice, e.Message.Choices[e.Answer.Choice-1])
		}
		return text
	}
	return e.State
}

// decisionText is one decision as a line, its question, options and answer
// indented beneath:
//
//	decision ID answered by sws: 2. no in #work from builder
//	  Ship with the simple parser?
//	  1. yes
//	  2. no
//	  sws: Decided 2 (no)
func decisionText(e irc.ChatEntry) string {
	var text strings.Builder
	text.WriteString("decision " + e.ID + " " + decisionState(e))
	text.WriteString(" in " + e.Target + " from " + e.From)
	if e.Message != nil {
		text.WriteString("\n  " + indentContinuation(e.Message.Message))
		for i, option := range e.Message.Choices {
			fmt.Fprintf(&text, "\n  %d. %s", i+1, option)
		}
	}
	if e.Answer != nil {
		text.WriteString("\n  " + e.Answer.From + ": " + indentContinuation(e.Answer.Message))
	}
	return text.String()
}
