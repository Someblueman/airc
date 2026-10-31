package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

const searchUsage = "usage: airc search QUERY [--target '#room'|@nick|thread:ID|*] [--from NICK] [--run ID] [--since 2h|2026-10-17] [--until TIME] [--newest] [--exact] [--after CURSOR] [--limit 50] [--json]"

func runSearchSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New(searchUsage)
	}
	query := args[0]
	fs := flag.NewFlagSet("airc search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	opt.session = session
	target := fs.String("target", os.Getenv("AIRC_CHANNEL"), "history target; defaults to AIRC_CHANNEL, or * for all retained messages")
	from := fs.String("from", "", "only this sender (case-insensitive)")
	after := fs.String("after", "*", "cursor from the previous page (a message ID), or * to start; with --newest a page continues before it")
	limit := fs.Int("limit", 50, "maximum matches, 1-1000")
	since := fs.String("since", "", "only messages at or after this time: a duration ago (90m, 2h, 3d, 1w), a date (2026-10-17) or RFC 3339")
	until := fs.String("until", "", "only messages before this time, in the same forms as --since")
	newest := fs.Bool("newest", false, "newest matches first (reads the memory window; older matches may remain in the archive)")
	exact := fs.Bool("exact", false, "match the query as one literal substring, not as terms")
	run := fs.String("run", "", "only messages posted under this run label (needs a daemon with RUN)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || strings.TrimSpace(query) == "" || !protocol.BriefText(query, 256) || *limit < 1 || *limit > 1000 || *after != "*" && !protocol.ValidMessageID(*after) {
		return errors.New("invalid search query, limit, or cursor")
	}
	if _, err := runLabel(*run); err != nil {
		return err
	}
	now := time.Now()
	var options irc.SearchOptions
	var err error
	if options.Since, err = parseSearchTime(*since, now); err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	if options.Until, err = parseSearchTime(*until, now); err != nil {
		return fmt.Errorf("--until: %w", err)
	}
	if !options.Since.IsZero() && !options.Until.IsZero() && !options.Since.Before(options.Until) {
		return errors.New("--since must be before --until")
	}
	options.Newest, options.Run = *newest, *run
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
				_, err = fmt.Fprintf(stdout, "%s %s %s [%s]: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, message.ID, indentContinuation(chatBody(message.ChatMetadata, message.ID, message.ReplyTo, message.From, message.Message)))
			}
			if err != nil {
				return err
			}
		}
		switch {
		case page.status == "more" && len(page.messages) > 0:
			_, err = fmt.Fprintf(stderr, "airc: more matches; continue with --after %s\n", page.messages[len(page.messages)-1].ID)
		case page.status == "archived":
			_, err = fmt.Fprintln(stderr, "airc: older matches may remain in the archive on disk; search without --newest (narrowed with --since) to reach them")
		}
		if err == nil && session != nil {
			return writePageStatus(stdout, page)
		}
		return err
	}
	native := func(ctx context.Context, client *irc.Client) error {
		search := options
		switch {
		case *exact:
		case client.Supports("SEARCH_TERMS"):
			search.Terms = true
		case !plainSearchWord(query):
			fmt.Fprintln(stderr, "airc: this aircd predates SEARCH_TERMS, so the query was matched as one exact substring; upgrade when safe")
		}
		if err := client.SearchWith(*target, query, *from, *after, *limit, search); err != nil {
			return err
		}
		page, err := awaitHistory(ctx, client, *target, nil)
		if err != nil {
			return err
		}
		return write(page)
	}
	refined := options.Newest || options.Run != "" || !options.Since.IsZero() || !options.Until.IsZero() || !*exact && !plainSearchWord(query)
	if *after != "*" || !protocol.IsChannel(*target) || refined {
		return chatRequestWithContext(ctx, *opt, "SEARCH", native)
	}
	// The first page of a room's matches for one word reads the same over any
	// carrier; the gateway matches a literal substring, which a single plain
	// word is either way.
	return readRacing(ctx, *opt, "SEARCH", native, func(ctx context.Context, conn *fallback.Conn) (fallback.Endpoint, error) {
		found, err := conn.Search(ctx, fallback.Search{Room: *target, Query: query, From: *from, Limit: *limit})
		if err != nil {
			return fallback.Endpoint{}, err
		}
		return found.Endpoint, write(fallbackPage(found))
	})
}

// plainSearchWord reports whether a query means the same as terms and as a
// literal substring: one word, with no quote or exclusion.
func plainSearchWord(query string) bool {
	query = strings.TrimSpace(query)
	return !strings.ContainsAny(query, " \t\n\"") && !strings.HasPrefix(query, "-")
}

// parseSearchTime reads a search time bound: empty for none, a duration
// before now (Go durations, plus d for days and w for weeks), a local date,
// or an RFC 3339 time.
func parseSearchTime(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for suffix, unit := range map[string]time.Duration{"d": 24 * time.Hour, "w": 7 * 24 * time.Hour} {
		if number, ok := strings.CutSuffix(value, suffix); ok {
			if n, err := strconv.Atoi(number); err == nil && n >= 0 && n <= 3650 {
				return now.Add(-time.Duration(n) * unit), nil
			}
		}
	}
	if d, err := time.ParseDuration(value); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", value, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	return time.Time{}, errors.New("want a duration ago (90m, 2h, 3d, 1w), a date (2026-10-17) or an RFC 3339 time")
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

// runReactSession adds a reaction to a message: it is a send with --reaction.
func runReactSession(ctx context.Context, session *agentConnection, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) < 2 || !protocol.ValidMessageID(args[0]) || !protocol.ValidReaction(args[1]) {
		return errors.New("usage: airc react MESSAGE_ID SYMBOL [--nick NICK] [--json]")
	}
	return runSendSession(ctx, session, append([]string{"--reply-to", args[0], "--reaction", args[1]}, args[2:]...), stdin, stdout, stderr)
}
