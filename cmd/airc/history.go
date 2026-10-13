package main

import (
	"cmp"
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

func runHistorySession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: airc history CHANNEL|NICK [--after MESSAGE_ID] [--limit 50] [--json]")
	}
	target := args[0]
	fs := flag.NewFlagSet("airc history", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	limit := fs.Int("limit", 50, "maximum messages to return")
	after := fs.String("after", "", "exclusive message ID cursor")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("history accepts one target followed by options")
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("--limit must be between 1 and 1000")
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	var client *irc.Client
	var err error
	if *after == "" && protocol.IsChannel(target) {
		// The latest page of a room reads the same over any carrier (their
		// cursors are not message IDs, so --after stays native): race native
		// IRC against the others and read over whichever connects first.
		r, err := connectRacing(ctx, *opt, true)
		if err != nil {
			return err
		}
		if r.native == nil {
			return historyViaFallback(ctx, *opt, r, target, *limit, session != nil, stdout)
		}
		client = r.native
	} else if client, err = dialPrimary(ctx, *opt); err != nil {
		return err
	}
	defer closeOneShot(*opt, client)
	defer closeOnCancel(ctx, client)()
	if _, _, conversation := protocol.ConversationTarget(target); conversation {
		if !client.Supports("REPLIES") {
			return errors.New("conversation history needs a daemon with REPLIES; upgrade/restart when active work is finished")
		}
		if *after == "" {
			*after = "*"
		}
	}
	if !client.Ephemeral() {
		// Older servers only serve channels, and only to members.
		if !protocol.IsChannel(target) {
			return errors.New("this aircd cannot read direct-message history; restart it from a current build")
		}
		if err := client.Join(target); err != nil {
			return err
		}
	}
	if target == irc.AllDirectMessages && !client.Supports("DM_AUDIT") {
		return errors.New("all-DM history needs a daemon with DM_AUDIT; upgrade/restart when active work is finished")
	}
	page, err := fetchHistory(ctx, client, target, *after, *limit, nil)
	if err != nil {
		return err
	}
	messages, status := page.messages, page.status
	switch {
	case status == "" && *after != "":
		// Older servers ignore the cursor and return the latest messages.
		cursor := -1
		for index, message := range messages {
			if message.ID == *after {
				cursor = index
				break
			}
		}
		if cursor < 0 {
			return fmt.Errorf("history cursor %q is not in the latest %d messages; it may have expired", *after, *limit)
		}
		messages = messages[cursor+1:]
	case status == "expired":
		if session != nil {
			_ = writePageStatus(stdout, page)
		}
		return fmt.Errorf("history cursor %q is no longer retained; read the latest messages without --after and continue from the newest ID", *after)
	}
	if err := writeHistory(stdout, *opt, target, messages); err != nil {
		return err
	}
	if status == "more" && len(messages) > 0 {
		fmt.Fprintf(stderr, "airc: more messages are available; continue with --after %s\n", messages[len(messages)-1].ID)
	}
	if session != nil {
		return writePageStatus(stdout, page)
	}
	return nil
}

// historyViaFallback prints the latest page of a room read over another carrier.
func historyViaFallback(ctx context.Context, opt options, r reached, room string, limit int, session bool, stdout io.Writer) error {
	read, err := r.conn.History(ctx, room, limit)
	if err != nil {
		return raceError(opt, err)
	}
	answeredBy(opt, r, read.Endpoint)
	page := fallbackPage(read)
	if err := writeHistory(stdout, opt, room, page.messages); err != nil {
		return err
	}
	if session {
		return writePageStatus(stdout, page)
	}
	return nil
}

func writeHistory(stdout io.Writer, opt options, target string, messages []*irc.HistoryEvent) error {
	var err error
	encoder := json.NewEncoder(stdout)
	for _, message := range messages {
		if opt.json {
			err = encoder.Encode(message)
		} else {
			_, err = fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, historyLabel(target, message), indentContinuation(chatBody(message.ChatMetadata, message.ID, message.From, message.Message)))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Reuse history's bounded paging and output without moving inbox cursors.
func runThread(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 || !protocol.ValidMessageID(args[0]) {
		return errors.New("usage: airc thread MESSAGE_ID [--after ID] [--limit 50] [--json]")
	}
	query := append([]string{"thread:" + args[0]}, args[1:]...)
	return runSessionCommand("history", query, nil, stdout, stderr)
}

func historyLabel(target string, message *irc.HistoryEvent) string {
	label := message.From
	if _, _, conversation := protocol.ConversationTarget(target); conversation {
		label += " [" + message.ID + "]"
	}
	if message.Reaction != "" {
		label += " (reacted " + message.Reaction + " to " + message.ReplyTo + ")"
	} else if message.ReplyTo != "" {
		label += " (reply to " + message.ReplyTo + ")"
	}
	return label
}

type historyPage struct {
	messages []*irc.HistoryEvent
	status   string
	cursor   string
}

// fetchHistory reads one reply, preserving unrelated live events for observers.
func fetchHistory(ctx context.Context, client *irc.Client, target, after string, limit int, other func(irc.Event)) (historyPage, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return historyPage{}, err
	}
	if err := client.HistoryAfter(target, after, limit); err != nil {
		if ctx.Err() != nil {
			return historyPage{}, ctx.Err()
		}
		return historyPage{}, err
	}
	return awaitHistory(ctx, client, target, other)
}

func awaitHistory(ctx context.Context, client *irc.Client, target string, other func(irc.Event)) (historyPage, error) {
	var page historyPage
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if ctx.Err() != nil {
					return historyPage{}, ctx.Err()
				}
				return historyPage{}, fmt.Errorf("server disconnected while reading history: %w", io.EOF)
			}
			switch value := event.(type) {
			case *irc.HistoryEvent:
				page.messages = append(page.messages, value)
				continue
			case *irc.EndOfHistoryEvent:
				if sameTarget(value.Target, target) {
					page.status, page.cursor = value.Status, value.Cursor
					return page, nil
				}
			}
			if err := serverError(event); err != nil {
				return historyPage{}, err
			}
			if other != nil {
				other(event)
			}
		case <-ctx.Done():
			return historyPage{}, ctx.Err()
		case <-client.Done():
			if ctx.Err() != nil {
				return historyPage{}, ctx.Err()
			}
			return historyPage{}, fmt.Errorf("connection closed while reading history: %w", io.EOF)
		}
	}
}

// compareHistory orders messages by time, then by server sequence.
func compareHistory(a, b *irc.HistoryEvent) int {
	return cmp.Or(a.Timestamp.Compare(b.Timestamp), cmp.Compare(a.Seq, b.Seq))
}

func writePageStatus(out io.Writer, page historyPage) error {
	return json.NewEncoder(out).Encode(struct {
		Type   string `json:"type"`
		Status string `json:"status"`
		Cursor string `json:"cursor"`
		Gap    bool   `json:"gap"`
	}{"page", page.status, page.cursor, page.status == "expired"})
}
