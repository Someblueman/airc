package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
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

type checkOptions struct {
	channels                              listFlag
	wait                                  time.Duration
	peek, includeOwn, mentions            bool
	limit, initial, maxMessages, maxBytes int
}

func addCheckOptions(fs *flag.FlagSet) *checkOptions {
	c := &checkOptions{}
	fs.Var(&c.channels, "channel", "channel to follow; repeat or comma-separate (env AIRC_CHANNEL)")
	fs.DurationVar(&c.wait, "wait", 0, "total time allowed for a blocking check (for example 60s)")
	fs.BoolVar(&c.peek, "peek", false, "show messages without marking them read")
	fs.IntVar(&c.limit, "limit", 100, "messages fetched per request (1-1000)")
	fs.IntVar(&c.initial, "initial", 20, "recent channel context on first check (inboxes start at the oldest retained message)")
	fs.IntVar(&c.maxMessages, "max-messages", 100, "maximum messages returned by one check (1-1000)")
	fs.IntVar(&c.maxBytes, "max-bytes", 32768, "maximum output bytes including metadata (1024-1048576); messages are never truncated")
	fs.BoolVar(&c.includeOwn, "include-own", false, "also return messages sent by this nickname")
	fs.BoolVar(&c.mentions, "mentions", false, "only direct messages and tags, from any channel")
	return c
}

func (c *checkOptions) targets(nick string) ([]checkTarget, error) {
	if c.limit < 1 || c.limit > 1000 || c.initial < 1 || c.initial > 1000 || c.wait < 0 ||
		c.maxMessages < 1 || c.maxMessages > 1000 || c.maxBytes < 1024 || c.maxBytes > 1<<20 {
		return nil, errors.New("--limit, --initial and --max-messages must be 1-1000; --max-bytes must be 1024-1048576; --wait must not be negative")
	}
	if len(c.channels) == 0 {
		_ = c.channels.Set(os.Getenv("AIRC_CHANNEL"))
	}
	seen := map[string]bool{}
	var targets []checkTarget
	for _, channel := range c.channels {
		channel = channelName(channel)
		if !isChannel(channel) {
			return nil, fmt.Errorf("%q is not a channel name", channel)
		}
		if !seen[channel] {
			seen[channel] = true
			targets = append(targets, checkTarget{name: channel, key: channel, watch: channel})
		}
	}
	if len(targets) > 63 {
		return nil, errors.New("a check can follow at most 63 channels plus its inbox")
	}
	return targets, nil
}

type checkTarget struct{ name, key, watch string }
type checkMessage struct {
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	Seq       uint64    `json:"seq,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Mentioned bool      `json:"mentioned,omitempty"`
}
type checkTopic struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Topic  string `json:"topic"`
}
type checkStatus struct {
	Type     string   `json:"type"`
	More     bool     `json:"more"`
	Gaps     []string `json:"gaps,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func runCheck(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt, settings := addOptions(fs), addCheckOptions(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc check [--nick NAME] [--channel ROOM] [--wait 60s] [--json]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	timeout := requestTimeout
	if settings.wait > 0 {
		timeout = settings.wait
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := checkWithClient(ctx, *opt, settings, nil, stdout, stderr)
	if settings.wait > 0 && errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

// checkWithClient permits send --check to reuse its connection. The cursor lock
// covers the entire read/output/save operation, including a blocking check.
func checkWithClient(ctx context.Context, opt options, settings *checkOptions, client *irc.Client, stdout, stderr io.Writer) error {
	followed, err := settings.targets(opt.nick)
	if err != nil {
		return err
	}
	store, err := openCursors(opt, opt.nick)
	if err != nil {
		return err
	}
	defer store.close()
	if client == nil {
		client, err = dialOneShot(ctx, opt)
		if err != nil {
			return err
		}
		defer client.Close()
	}
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	if !client.Ephemeral() {
		return errors.New("this aircd predates airc check; upgrade the daemon when its active work is finished")
	}
	if value, known := client.Features()["HISTORY"]; known && value == "0" {
		return errors.New("aircd history is disabled; check cannot retrieve messages (start the daemon with --history N when safe)")
	}
	inbox := checkTarget{name: opt.nick, key: "@" + strings.ToLower(opt.nick), watch: "@" + opt.nick}
	if client.Supports("MENTIONS") {
		inbox.name = "@" + opt.nick
	} else if settings.mentions {
		return errors.New("this aircd predates mentions; upgrade it when safe; ordinary check still reads followed channels and direct messages")
	} else {
		fmt.Fprintln(stderr, "airc: warning: this daemon has no cross-channel mentions; only followed channels and direct messages are checked (see airc doctor)")
	}
	targets := append(followed, inbox)
	if settings.mentions {
		targets = []checkTarget{inbox}
	}
	c := &checker{client: client, nick: opt.nick, settings: settings, targets: targets}
	var headers []checkTopic
	if client.Supports("TOPIC") && !settings.mentions {
		for _, target := range followed {
			text, err := fetchTopic(ctx, client, target.name, c.noteLive)
			if err != nil {
				return err
			}
			if text != store.Topics[target.name] {
				headers = append(headers, checkTopic{Type: "topic", Target: target.name, Topic: text})
			}
		}
	}
	if settings.wait > 0 {
		for start := 0; start < len(targets); start += 16 {
			chunk := targets[start:min(start+16, len(targets))]
			names := make([]string, len(chunk))
			for i, target := range chunk {
				names[i] = target.watch
			}
			if err := client.Observe(names...); err != nil {
				return err
			}
			if err := awaitObservationWith(ctx, client, len(names), c.noteLive); err != nil {
				return err
			}
		}
	}
	for {
		c.sawLive = false
		batch, err := c.fetchNew(ctx, store.Cursors)
		if err != nil {
			return err
		}
		if batch.hasVisible || batch.more || len(headers) > 0 || settings.wait == 0 || ctx.Err() != nil {
			return c.output(batch, headers, store, opt.json, stdout, stderr)
		}
		if c.sawLive {
			continue
		}
		if err := c.waitForLive(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return c.output(batch, headers, store, opt.json, stdout, stderr)
			}
			return err
		}
	}
}
