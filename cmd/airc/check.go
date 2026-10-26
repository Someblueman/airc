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

const maxCheckPages = 50

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(value string) error {
	*l = append(*l, splitList(value)...)
	return nil
}

// splitList splits a comma-separated flag value, trimming each item and
// dropping empty ones.
func splitList(value string) []string {
	var items []string
	for item := range strings.SplitSeq(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			items = append(items, item)
		}
	}
	return items
}

type checkOptions struct {
	channels                              listFlag
	replyTo, decision                     string
	wait                                  time.Duration
	peek, includeOwn, mentions            bool
	compact, fromNow                      bool
	limit, initial, maxMessages, maxBytes int
}

func addCheckOptions(fs *flag.FlagSet) *checkOptions {
	c := &checkOptions{}
	fs.Var(&c.channels, "channel", "channel to follow; repeat or comma-separate (env AIRC_CHANNEL)")
	fs.StringVar(&c.replyTo, "reply-to", "", "only immediate replies to this message ID (keeps room/inbox cursors unchanged)")
	fs.StringVar(&c.decision, "decision", "", "wait for this decision to be answered or withdrawn and print it (with --wait; moves no cursors)")
	fs.DurationVar(&c.wait, "wait", 0, "total time allowed for a blocking check (for example 60s)")
	fs.BoolVar(&c.peek, "peek", false, "show messages without marking them read")
	fs.IntVar(&c.limit, "limit", 100, "legacy history page size (1-1000); combined checks use the output budget")
	fs.IntVar(&c.initial, "initial", 20, "recent channel context on first check (inboxes start at the oldest retained message)")
	fs.IntVar(&c.maxMessages, "max-messages", 100, "maximum messages returned by one check (1-1000)")
	fs.IntVar(&c.maxBytes, "max-bytes", 32768, "maximum output bytes including metadata (1024-1048576); messages are never truncated")
	fs.BoolVar(&c.includeOwn, "include-own", false, "also return messages sent by this nickname")
	fs.BoolVar(&c.mentions, "mentions", false, "only direct messages and tags, from any channel")
	fs.BoolVar(&c.compact, "compact", false, "with --json, omit seq, request_id and account_id and shorten timestamps to seconds")
	fs.BoolVar(&c.fromNow, "from-now", false, "mark everything retained as read without printing it; room headers and pins are still shown")
	return c
}

func (c *checkOptions) targets(nick string) ([]checkTarget, error) {
	if c.limit < 1 || c.limit > 1000 || c.initial < 1 || c.initial > 1000 || c.wait < 0 ||
		c.maxMessages < 1 || c.maxMessages > 1000 || c.maxBytes < 1024 || c.maxBytes > 1<<20 {
		return nil, errors.New("--limit, --initial and --max-messages must be 1-1000; --max-bytes must be 1024-1048576; --wait must not be negative")
	}
	if c.fromNow && (c.wait > 0 || c.peek || c.replyTo != "") {
		return nil, errors.New("--from-now cannot be combined with --wait, --peek or --reply-to")
	}
	if c.replyTo != "" {
		if !protocol.ValidMessageID(c.replyTo) || len(c.channels) > 0 || c.mentions {
			return nil, errors.New("--reply-to requires a message ID and cannot be combined with --channel or --mentions")
		}
		name := "replies:" + c.replyTo
		return []checkTarget{{name: name, key: name, watch: name}}, nil
	}
	if len(c.channels) == 0 && !c.mentions {
		_ = c.channels.Set(os.Getenv("AIRC_CHANNEL"))
	}
	seen := map[string]bool{}
	var targets []checkTarget
	for _, channel := range c.channels {
		channel = channelName(channel)
		if !protocol.IsChannel(channel) {
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
	protocol.ChatMetadata
	Type      string    `json:"type"`
	ID        string    `json:"id"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	Seq       uint64    `json:"seq,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
	Mentioned bool      `json:"mentioned,omitempty"`
}
type checkTopic struct {
	Type        string `json:"type"`
	Target      string `json:"target"`
	Topic       string `json:"topic"`
	ID          string `json:"id,omitempty"`
	From        string `json:"from,omitempty"`
	PinKey      string `json:"-"`
	Fingerprint string `json:"-"`
}
type checkStatus struct {
	Type      string   `json:"type"`
	Code      string   `json:"code"`
	Retryable bool     `json:"retryable"`
	More      bool     `json:"more"`
	Skipped   int      `json:"skipped,omitempty"`
	Gaps      []string `json:"gaps,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func runCheckSession(ctx context.Context, session *agentConnection, args []string, stdout, stderr io.Writer) (resultErr error) {
	fs := flag.NewFlagSet("airc check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt, settings := addOptions(fs), addCheckOptions(fs)
	opt.session = session
	defer func() { reportFailure(&resultErr, opt.json, stderr) }()
	if err := parseAgentFlags(fs, args, opt, stderr); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: airc check [--nick NAME] [--channel ROOM] [--wait 60s] [--json]")
	}
	if err := identity(opt); err != nil {
		return err
	}
	if settings.decision != "" {
		if err := settings.decisionOnly(fs); err != nil {
			return err
		}
		// The wait bounds the watch; the lookups around it get their own time.
		ctx, cancel := context.WithTimeout(ctx, settings.wait+requestTimeout)
		defer cancel()
		return waitForDecision(ctx, *opt, settings.decision, settings.wait, stdout)
	}
	timeout := requestTimeout
	if settings.wait > 0 {
		timeout = settings.wait
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if settings.fromNow {
		if _, err := settings.targets(opt.nick); err != nil {
			return err
		}
		return checkFromNow(ctx, *opt, settings, stdout, stderr)
	}
	return checkWithClient(ctx, *opt, settings, nil, stdout, stderr)
}

// decisionOnly checks that --decision came with nothing but --wait: it reads
// one decision rather than cursors, so every other check option is a mistake.
func (c *checkOptions) decisionOnly(fs *flag.FlagSet) error {
	others := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "channel", "reply-to", "peek", "limit", "initial", "max-messages", "max-bytes", "include-own", "mentions", "compact", "from-now":
			others = true
		}
	})
	if others || !protocol.ValidMessageID(c.decision) || c.wait < 0 {
		return errors.New("--decision requires a message ID and cannot be combined with --channel, --mentions, --reply-to or other check options")
	}
	return nil
}

// waitForDecision prints decision id, waiting up to wait for it to be
// answered or withdrawn first: one connection, the place its steps are
// posted observed, and the row read again once a step arrives, so an answer
// that raced the observation is not missed. The decision outlives the
// history ring, so the wait watches the room, or this nick's inbox for a
// direct decision, rather than the offer's thread, which the server no
// longer knows once the offer has been evicted. With --json the row is
// followed by a status: decided when it is final, wait_expired when it is
// still open.
func waitForDecision(ctx context.Context, opt options, id string, wait time.Duration, stdout io.Writer) error {
	return chatRequestWithContext(ctx, opt, "DECISION", func(ctx context.Context, client *irc.Client) error {
		fetch := func() (irc.ChatEntry, error) {
			entries, err := irc.RequestChat(ctx, client, irc.ChatRequest{Action: "decisions", ID: id}, nil)
			if err != nil {
				return irc.ChatEntry{}, err
			}
			if len(entries) != 1 {
				return irc.ChatEntry{}, errors.New("decision is not retained")
			}
			return entries[0], nil
		}
		entry, err := fetch()
		if err != nil {
			return err
		}
		if entry.State == "open" && wait > 0 {
			target := entry.Target
			if !protocol.IsChannel(target) {
				target = "@" + opt.nick
			}
			if err := client.Observe(target); err != nil {
				return err
			}
			if err := awaitObservationWith(ctx, client, 1, nil, false); err != nil {
				return err
			}
			if entry, err = fetch(); err != nil {
				return err
			}
		}
		if entry.State == "open" && wait > 0 {
			err := awaitEvent(ctx, client, wait, "waiting for the decision", "", func(event irc.Event) (bool, error) {
				m, ok := event.(*irc.MessageEvent)
				return ok && m.ReplyTo == id && (m.Kind == "decision-answer" || m.Kind == "decision-withdraw"), nil
			})
			if err != nil && !errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if err == nil {
				if entry, err = fetch(); err != nil {
					return err
				}
			}
		}
		if !opt.json {
			_, err := fmt.Fprintln(stdout, decisionText(entry))
			return err
		}
		if err := json.NewEncoder(stdout).Encode(newDecisionRow(entry)); err != nil {
			return err
		}
		status := checkStatus{Type: "status", Code: "decided"}
		if entry.State == "open" {
			status.Code = "wait_expired"
		}
		return json.NewEncoder(stdout).Encode(status)
	})
}

// compacted drops the fields an agent rarely needs for reading and replying.
func (m checkMessage) compacted() checkMessage {
	m.Seq, m.RequestID, m.AccountID = 0, "", ""
	m.Timestamp = m.Timestamp.Truncate(time.Second)
	return m
}

// checkWithClient permits send --check to reuse its connection. The cursor lock
// covers each read/output/save operation. Idle waits release it.
func checkWithClient(ctx context.Context, opt options, settings *checkOptions, client *irc.Client, stdout, stderr io.Writer) error {
	retry := newBackoff(250*time.Millisecond, 5*time.Second, true)
	for {
		err := checkAttempt(ctx, opt, settings, client, stdout, stderr)
		if err == nil {
			return nil
		}
		if switched, ok := errors.AsType[*overCarriers](err); ok {
			back, err := checkOverCarriers(ctx, opt, settings, switched.fb, switched.conn, stdout, stderr)
			if back == nil {
				return err
			}
			// Native IRC registered again during the wait: continue there,
			// where the inbox is.
			defer back.Close()
			client = back
			continue
		}
		if ctx.Err() != nil {
			return failure(ctx.Err(), failure(err, "wait").Phase)
		}
		if settings.wait == 0 || !failure(err, "wait").Retryable {
			return err
		}
		// The original deadline bounds retries, including login and observation.
		client = nil
		if !retry.wait(ctx) {
			// Name the step that kept failing, not the pause between attempts.
			return failure(ctx.Err(), failure(err, "reconnect").Phase)
		}
	}
}

func checkAttempt(ctx context.Context, opt options, settings *checkOptions, client *irc.Client, stdout, stderr io.Writer) error {
	followed, err := settings.targets(opt.nick)
	if err != nil {
		return err
	}
	store, err := openCursors(opt, opt.nick)
	if err != nil {
		return err
	}
	defer func() {
		if store != nil {
			store.close()
		}
	}()
	if store.warning != "" {
		fmt.Fprintln(stderr, "airc: warning:", store.warning)
	}
	if client == nil && carrierCheckable(settings) {
		// Native IRC leads by two seconds, since only it serves the inbox;
		// the rooms continue over another carrier when it does not answer.
		r, err := connectRacing(ctx, opt, nativeLead)
		if err != nil {
			return failure(err, "login")
		}
		if r.native == nil {
			return &overCarriers{fb: r.fb, conn: r.conn}
		}
		client = r.native
		defer closeOneShot(opt, client)
	} else if client == nil {
		client, err = dialOneShot(ctx, opt)
		if err != nil {
			return failure(err, "login")
		}
		defer closeOneShot(opt, client)
	}
	defer closeOnCancel(ctx, client)()
	if !client.Ephemeral() {
		return errors.New("this aircd predates airc check; upgrade the daemon when its active work is finished")
	}
	if settings.replyTo != "" && !client.Supports("REPLIES") {
		return errors.New("reply checks need a daemon with REPLIES; upgrade/restart when active work is finished")
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
	if settings.replyTo == "" && !settings.mentions {
		for _, id := range store.Follows {
			targets = append(targets, checkTarget{name: "thread:" + id, key: "thread:" + id, watch: "thread:" + id})
		}
	}
	if settings.replyTo != "" {
		targets = followed
	} else if settings.mentions {
		targets = []checkTarget{inbox}
	}
	if settings.wait > 0 && len(targets) > 64 {
		return errors.New("blocking checks support 64 targets including inbox and followed threads; choose fewer rooms or follows")
	}
	c := &checker{client: client, nick: opt.nick, settings: settings, targets: targets}
	if len(store.Shown) > 0 {
		c.shown = map[string]bool{}
		for _, id := range store.Shown {
			c.shown[id] = true
		}
	}
	var headers []checkTopic
	combined := client.Supports("CHECK") && len(targets) <= 64
	if combined {
		data, _ := json.Marshal(c.combinedRequest(store, followed))
		combined = len(data)+len("CHECK :") <= protocol.MaxLineLength
	}
	if !combined && client.Supports("CHAT") && !settings.mentions && settings.replyTo == "" {
		pins, err := checkPins(ctx, client, followed, store, c.noteLive)
		if err != nil {
			return err
		}
		headers = append(headers, pins...)
	}
	if !combined && client.Supports("TOPIC") && !settings.mentions && settings.replyTo == "" {
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
			if err := awaitObservationWith(ctx, client, len(names), c.noteLive, true); err != nil {
				return err
			}
		}
	}
	for {
		c.sawLive = false
		var batch checkBatch
		var err error
		if combined {
			var rooms []checkTarget
			if !settings.mentions && settings.replyTo == "" {
				rooms = followed
			}
			batch, headers, err = c.fetchCombined(ctx, store, rooms)
		} else {
			batch, err = c.fetchNew(ctx, store.Cursors)
		}
		if err != nil {
			return failure(err, "history")
		}
		if !batch.hasVisible && !batch.more && len(headers) == 0 && ctx.Err() != nil {
			return failure(ctx.Err(), "history")
		}
		if batch.hasVisible || batch.more || len(headers) > 0 || len(batch.gaps) > 0 || len(batch.warnings) > 0 || settings.wait == 0 {
			return c.output(batch, headers, store, opt.json, stdout, stderr)
		}
		if c.sawLive {
			continue
		}
		// Commit filtered messages before releasing the lock. A concurrent check
		// may advance further; reload its state before fetching again.
		if err := c.output(batch, nil, store, false, io.Discard, stderr); err != nil {
			return err
		}
		store.close()
		store = nil
		if err := c.waitForLive(ctx); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				if opt.json {
					return json.NewEncoder(stdout).Encode(checkStatus{Type: "status", Code: "wait_expired"})
				}
				return nil
			}
			return err
		}
		store, err = openCursors(opt, opt.nick)
		if err != nil {
			return err
		}
	}
}
