package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// runDecideSession asks a named decider, usually a human, to decide
// something, or with a step word and an ID answers or withdraws a decision.
func runDecideSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	in := decisionInput{}
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		in.Step, args = args[0], args[1:]
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			in.ID, args = args[0], args[1:]
		}
	}
	fs := flag.NewFlagSet("airc decide", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	fs.StringVar(&in.Channel, "channel", "", "room to ask in (env AIRC_CHANNEL); needs --from")
	fs.StringVar(&in.From, "from", "", "nickname who decides, usually a human; required in a room")
	fs.StringVar(&in.To, "to", "", "nickname who decides, as a direct decision")
	fs.StringVar(&in.Message, "message", "", "what to decide and why it matters, or a note on the step; - reads stdin")
	fs.Var((*pollChoices)(&in.Options), "option", "a choice to decide between, numbered from 1; repeat for 2-8 options")
	fs.IntVar(&in.Choice, "choice", 0, "answer: the chosen option's number")
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
	return chatEntries(ctx, *opt, r, stdout, stderr)
}

// warnDeciderKind reads nick's directory card over client and warns when its
// kind is not human. It never refuses: a lookup that fails is one line too.
func warnDeciderKind(ctx context.Context, client *irc.Client, nick string, stderr io.Writer) {
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
	Run             string    `json:"run,omitempty"`
}

func newDecisionRow(e irc.ChatEntry) decisionRow {
	row := decisionRow{Type: "decision", ID: e.ID, Target: e.Target, From: e.From, State: e.State}
	if e.Message != nil {
		row.Decider, row.Message, row.Options, row.Timestamp, row.Run = e.Message.For, e.Message.Message, e.Message.Choices, e.Message.Timestamp, e.Message.Run
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
