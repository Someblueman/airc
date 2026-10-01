package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const maxCheckPages = 50

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(value string) error {
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			*l = append(*l, item)
		}
	}
	return nil
}

type checkTarget struct {
	name  string // what the history request uses: a channel or a bare nick
	key   string // cursor key
	watch string // observe target: a channel or @nick
}

type checkMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Seq       uint64    `json:"seq,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// runCheck returns everything new for an agent since its previous check: the
// channels it follows plus direct messages addressed to its nickname. It keeps
// its own cursors, so an agent just calls it each turn (optionally with --wait
// to block for a reply) and never holds a connection or remembers message IDs.
func runCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	var channels listFlag
	fs.Var(&channels, "channel", "channel to follow; repeat or comma-separate (env AIRC_CHANNEL)")
	wait := fs.Duration("wait", 0, "if nothing is new, wait up to this long for a message (for example 60s)")
	peek := fs.Bool("peek", false, "show new messages without marking them as read")
	limit := fs.Int("limit", 100, "messages fetched per request (1-1000); more are fetched automatically")
	initial := fs.Int("initial", 20, "recent messages to show for a channel on its first check")
	includeOwn := fs.Bool("include-own", false, "also return messages sent by this nickname")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc check [--nick NAME] [--channel #room]... [--wait 60s] [--peek] [--json]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	if len(channels) == 0 {
		_ = channels.Set(os.Getenv("AIRC_CHANNEL"))
	}
	if *limit < 1 || *limit > 1000 || *initial < 1 || *initial > 1000 || *wait < 0 {
		return errors.New("--limit and --initial must be 1-1000 and --wait must not be negative")
	}
	targets := []checkTarget{}
	seen := map[string]bool{}
	for _, channel := range channels {
		if !isChannel(channel) {
			return fmt.Errorf("%q is not a channel name (channels start with # or &)", channel)
		}
		if !seen[channel] {
			seen[channel] = true
			targets = append(targets, checkTarget{name: channel, key: channel, watch: channel})
		}
	}
	targets = append(targets, checkTarget{name: opt.nick, key: "@" + strings.ToLower(opt.nick), watch: "@" + opt.nick})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, err := openCursors(*opt, opt.nick)
	if err != nil {
		return err
	}
	defer store.close()
	client, err := dialOneShot(*opt)
	if err != nil {
		return err
	}
	defer client.Close()
	if !client.Ephemeral() {
		return errors.New("this aircd predates `airc check`; restart it from a current build, or use `airc history --after` meanwhile")
	}

	c := &checker{client: client, nick: opt.nick, includeOwn: *includeOwn, targets: targets, limit: *limit, initial: *initial, stderr: stderr}
	if *wait > 0 {
		names := make([]string, len(targets))
		for i, target := range targets {
			names[i] = target.watch
		}
		// Subscribe before reading history so nothing can slip between the two.
		if err := client.Observe(names...); err != nil {
			return err
		}
		if err := awaitObservation(ctx, client, len(names)); err != nil {
			return err
		}
	}

	cursors := make(map[string]string, len(store.Cursors))
	for key, id := range store.Cursors {
		cursors[key] = id
	}
	deadline := time.Now().Add(*wait)
	var visible []*irc.HistoryEvent
	for {
		c.sawLive = false
		fetched, err := c.fetchNew(cursors)
		if err != nil {
			return err
		}
		for _, message := range fetched {
			if c.includeOwn || !strings.EqualFold(message.From, opt.nick) {
				visible = append(visible, message)
			}
		}
		if len(visible) > 0 || *wait == 0 || !time.Now().Before(deadline) {
			break
		}
		if c.sawLive {
			continue // a live message arrived while reading history; read again
		}
		if err := c.waitForLive(ctx, deadline); err != nil {
			return err
		}
	}

	sort.SliceStable(visible, func(i, j int) bool {
		if !visible[i].Timestamp.Equal(visible[j].Timestamp) {
			return visible[i].Timestamp.Before(visible[j].Timestamp)
		}
		return visible[i].Seq < visible[j].Seq
	})
	encoder := json.NewEncoder(stdout)
	for _, message := range visible {
		if opt.json {
			err = encoder.Encode(checkMessage{Type: "message", ID: message.ID, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp})
		} else {
			_, err = fmt.Fprintf(stdout, "%s %s %s: %s\n", message.Timestamp.Format(time.RFC3339), message.Target, message.From, indentContinuation(message.Message))
		}
		if err != nil {
			return err // not saved: the next check returns these again
		}
	}
	if *peek {
		return nil
	}
	return store.save(cursors)
}

type checker struct {
	client     *irc.Client
	nick       string
	includeOwn bool
	targets    []checkTarget
	limit      int
	initial    int
	stderr     io.Writer
	sawLive    bool
}

// fetchNew reads every target after its cursor and advances cursors in place.
func (c *checker) fetchNew(cursors map[string]string) ([]*irc.HistoryEvent, error) {
	var all []*irc.HistoryEvent
	for _, target := range c.targets {
		after := cursors[target.key]
		for page := 0; ; page++ {
			limit := c.limit
			if after == "" {
				limit = c.initial
			}
			messages, status, err := fetchHistory(c.client, target.name, after, limit, c.noteLive)
			if err != nil {
				return nil, err
			}
			if status == "expired" {
				fmt.Fprintf(c.stderr, "airc: warning: the last message read from %s is no longer retained; messages may have been missed. Showing the latest instead.\n", target.name)
			}
			all = append(all, messages...)
			if len(messages) > 0 {
				after = messages[len(messages)-1].ID
				cursors[target.key] = after
			}
			if status != "more" || page+1 >= maxCheckPages {
				break
			}
		}
	}
	return all, nil
}

func (c *checker) noteLive(event irc.Event) {
	if message, ok := event.(*irc.MessageEvent); ok && (c.includeOwn || !strings.EqualFold(message.From, c.nick)) {
		c.sawLive = true
	}
}

// waitForLive blocks until a relevant live message arrives or the deadline passes.
func (c *checker) waitForLive(ctx context.Context, deadline time.Time) error {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case event, ok := <-c.client.Events():
			if !ok {
				return errors.New("server disconnected while waiting for messages")
			}
			if c.noteLive(event); c.sawLive {
				return nil
			}
		case <-timer.C:
			return nil
		case <-c.client.Done():
			return errors.New("server disconnected while waiting for messages")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func awaitObservation(ctx context.Context, client *irc.Client, count int) error {
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for acknowledged := 0; acknowledged < count; {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return errors.New("server disconnected before observation started")
			}
			if raw, isRaw := event.(*irc.RawEvent); isRaw && raw.Command == "765" {
				acknowledged++
			} else if err := serverError(event); err != nil {
				return fmt.Errorf("cannot wait for messages: %w", err)
			}
		case <-client.Done():
			return errors.New("server disconnected before observation started")
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("timed out waiting for server to confirm observation")
		}
	}
	return nil
}
