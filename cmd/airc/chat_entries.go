package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// entryShape is how one CHAT action's entries print: the feature the daemon
// must have (protocol.ChatFeature, so lacksCapability names what an older
// daemon is missing), each entry's JSON row and text line, and what text
// mode says to an empty listing ("" says nothing).
type entryShape struct {
	capability string
	row        func(irc.ChatEntry) any
	text       func(irc.ChatEntry) string
	empty      string
}

// shapeOf is the shape of r's entries: handoffs, decisions, statuses and
// resolutions have rows of their own; every other action prints the entry.
func shapeOf(r irc.ChatRequest) entryShape {
	capability := protocol.ChatFeature(r.Action)
	switch {
	case strings.HasPrefix(r.Action, "handoff"):
		shape := entryShape{capability, func(e irc.ChatEntry) any { return newHandoffRow(e) }, handoffText, ""}
		if r.Action == "handoffs" {
			shape.empty = "No handoffs."
		}
		return shape
	case strings.HasPrefix(r.Action, "decision"):
		shape := entryShape{capability, func(e irc.ChatEntry) any { return newDecisionRow(e) }, decisionText, ""}
		if r.Action == "decisions" {
			shape.empty = "No decisions."
		}
		return shape
	case r.Action == "status", r.Action == "statuses":
		shape := entryShape{capability, func(e irc.ChatEntry) any { return newStatusRow(e) }, statusText, ""}
		if r.Action == "statuses" {
			shape.empty = "No statuses in " + r.Target + "."
		}
		return shape
	case r.Action == "resolve":
		return entryShape{capability, func(e irc.ChatEntry) any { return newResolveRow(e) }, resolveText, ""}
	}
	return entryShape{capability, func(e irc.ChatEntry) any { return e }, chatEntryText, ""}
}

// chatEntries sends r over a connection and prints the entries it returns
// in the shape of r's action. Before asking a decision it looks the decider
// up and warns on stderr when they are not marked as a human: a decision is
// meant for a person, but an agent may decide too.
func chatEntries(ctx context.Context, opt options, r irc.ChatRequest, stdout, stderr io.Writer) error {
	shape := shapeOf(r)
	return chatRequestWithContext(ctx, opt, shape.capability, func(ctx context.Context, client *irc.Client) error {
		if r.Action == "decision" {
			warnDeciderKind(ctx, client, r.For, stderr)
		}
		entries, err := irc.RequestChat(ctx, client, r, nil)
		if err != nil {
			return err
		}
		return emitEntries(stdout, entries, opt.json, shape)
	})
}

// emitEntries prints entries one per line: as JSON rows with --json, as text
// otherwise, or the shape's word for none.
func emitEntries(stdout io.Writer, entries []irc.ChatEntry, machine bool, shape entryShape) error {
	if !machine && len(entries) == 0 && shape.empty != "" {
		_, err := fmt.Fprintln(stdout, shape.empty)
		return err
	}
	for _, e := range entries {
		var err error
		if machine {
			err = json.NewEncoder(stdout).Encode(shape.row(e))
		} else {
			_, err = fmt.Fprintln(stdout, shape.text(e))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runListingSession lists handoffs or decisions, as name says: those that
// concern you, a room's, or one by ID.
func runListingSession(ctx context.Context, session *agentConnection, name string, args []string, stdout, stderr io.Writer) error {
	noun := strings.TrimSuffix(name, "s")
	in := listingInput{}
	fs := flag.NewFlagSet("airc "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	fs.StringVar(&in.Channel, "channel", "", "every "+noun+" in this room, finished ones from the last week included")
	fs.StringVar(&in.ID, "id", "", "one "+noun)
	fs.IntVar(&in.Limit, "limit", 0, "newest "+name+" to list, at most 1000 (default 200)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	r, err := in.request(name)
	if err != nil {
		return err
	}
	if r.Target != "" || r.ID != "" {
		err = queryIdentity(opt)
	} else {
		err = identity(opt)
	}
	if err != nil {
		return err
	}
	return chatEntries(ctx, *opt, r, stdout, stderr)
}
