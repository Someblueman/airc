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

func runSearch(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: airc search QUERY [--target '#room'|@nick|thread:ID|*] [--from NICK] [--after ID] [--limit 50] [--json]")
	}
	query := args[0]
	fs := flag.NewFlagSet("airc search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
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
	}
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	return chatRequest(*opt, "SEARCH", func(ctx context.Context, client *irc.Client) error {
		if err := client.Search(*target, query, *from, *after, *limit); err != nil {
			return err
		}
		page, err := awaitHistory(ctx, client, *target, nil)
		if err != nil {
			return err
		}
		if page.status == "expired" {
			return errors.New("search cursor is no longer retained; restart with --after '*'")
		}
		for _, message := range page.messages {
			if opt.json {
				err = json.NewEncoder(stdout).Encode(message)
			} else {
				_, err = fmt.Fprintf(stdout, "%s %s %s [%s]: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, message.ID, indentContinuation(message.Message))
			}
			if err != nil {
				return err
			}
		}
		if page.status == "more" && len(page.messages) > 0 {
			_, err = fmt.Fprintf(stderr, "airc: more matches; continue with --after %s\n", page.messages[len(page.messages)-1].ID)
		}
		return err
	})
}

func runReact(args []string, stdout, stderr io.Writer) error {
	if len(args) < 2 || !protocol.ValidMessageID(args[0]) || !protocol.ValidReaction(args[1]) {
		return errors.New("usage: airc react MESSAGE_ID seen|checking|agree|disagree [--nick NICK] [--json]")
	}
	return runSend(append([]string{"--reply-to", args[0], "--reaction", args[1]}, args[2:]...), strings.NewReader(""), stdout, stderr)
}
