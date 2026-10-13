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
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

func runSearchSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: airc search QUERY [--target '#room'|@nick|thread:ID|*] [--from NICK] [--after ID] [--limit 50] [--json]")
	}
	query := args[0]
	fs := flag.NewFlagSet("airc search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	target := fs.String("target", os.Getenv("AIRC_CHANNEL"), "history target; defaults to AIRC_CHANNEL, or * for all retained messages")
	from := fs.String("from", "", "only this sender (case-insensitive)")
	after := fs.String("after", "*", "exclusive message ID, or * for oldest retained")
	limit := fs.Int("limit", 50, "maximum matches, 1-1000")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(query) == "" || !protocol.BriefText(query, 256) || *limit < 1 || *limit > 1000 || *after != "*" && !protocol.ValidMessageID(*after) {
		return errors.New("invalid search query, limit, or cursor")
	}
	if *target == "" {
		*target = "*"
	} else if *target != "*" && !strings.HasPrefix(*target, "thread:") {
		*target = channelName(*target)
	}
	if err := queryIdentity(opt); err != nil {
		return err
	}
	write := func(page historyPage) error {
		var err error
		if page.status == "expired" {
			if session != nil {
				_ = writePageStatus(stdout, page)
			}
			return errors.New("search cursor is no longer retained; restart with --after '*'")
		}
		for _, message := range page.messages {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(message)
			} else {
				_, err = fmt.Fprintf(stdout, "%s %s %s [%s]: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, message.ID, indentContinuation(chatBody(message.ChatMetadata, message.ID, message.From, message.Message)))
			}
			if err != nil {
				return err
			}
		}
		if page.status == "more" && len(page.messages) > 0 {
			_, err = fmt.Fprintf(stderr, "airc: more matches; continue with --after %s\n", page.messages[len(page.messages)-1].ID)
		}
		if err == nil && session != nil {
			return writePageStatus(stdout, page)
		}
		return err
	}
	native := func(ctx context.Context, client *irc.Client) error {
		if err := client.Search(*target, query, *from, *after, *limit); err != nil {
			return err
		}
		page, err := awaitHistory(ctx, client, *target, nil)
		if err != nil {
			return err
		}
		return write(page)
	}
	if *after != "*" || !protocol.IsChannel(*target) {
		return chatRequestWithContext(ctx, *opt, "SEARCH", native)
	}
	// The first page of a room's matches reads the same over any carrier.
	return readRacing(ctx, *opt, "SEARCH", native, func(ctx context.Context, conn *fallback.Conn) (fallback.Endpoint, error) {
		found, err := conn.Search(ctx, fallback.Search{Room: *target, Query: query, From: *from, Limit: *limit})
		if err != nil {
			return fallback.Endpoint{}, err
		}
		return found.Endpoint, write(fallbackPage(found))
	})
}

// fallbackPage is a carrier's page in the shape of a native one. Its cursor
// is the newest message ID, which native IRC's --after accepts; a carrier's
// own cursors are not message IDs.
func fallbackPage(read *fallback.Page) historyPage {
	page := historyPage{status: read.Status}
	for _, m := range read.Messages {
		page.messages = append(page.messages, historyEvent(m))
	}
	if len(page.messages) > 0 {
		page.cursor = page.messages[len(page.messages)-1].ID
	}
	return page
}

func runReact(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || !protocol.ValidMessageID(args[0]) || !protocol.ValidReaction(args[1]) {
		return errors.New("usage: airc react MESSAGE_ID SYMBOL [--nick NICK] [--json]")
	}
	return runSessionCommand("send", append([]string{"--reply-to", args[0], "--reaction", args[1]}, args[2:]...), strings.NewReader(""), stdout, stderr)
}
