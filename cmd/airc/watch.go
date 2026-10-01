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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// Reconnect delays; variables so tests can shorten them.
var watchMinBackoff, watchMaxBackoff = 500 * time.Millisecond, 10 * time.Second

const (
	defaultBacklog = 30
	seenLimit      = 4096
)

func runWatch(args []string, stdout, stderr io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWatchContext(ctx, args, stdout, stderr)
}

type watchTarget struct {
	observe string // what OBSERVE takes: a channel or @nick
	history string // what HISTORY takes: a channel or a bare nick
}

// watcher streams a room to a terminal or a program. It remembers the newest
// message it has shown per target, so after a dropped connection it resumes from
// there instead of losing what was said in the meantime, and it never shows a
// message twice.
type watcher struct {
	opt     options
	view    *renderer
	out     io.Writer
	json    bool
	targets []watchTarget
	backlog int

	encoder   *json.Encoder
	last      map[string]string // newest shown message ID per history target
	seen      map[string]struct{}
	seenOrder []string
	down      bool // an outage has been reported and not yet recovered
}

func runWatchContext(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("airc watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opt := addOptions(fs)
	channel := fs.String("channel", "", "channel, @nick, or comma-separated list to watch")
	colorMode := fs.String("color", "auto", "colored output: auto, always, or never (auto also honors NO_COLOR)")
	width := fs.Int("width", 0, "wrap text to this many columns (default: terminal width)")
	backlog := fs.Int("backlog", -1, fmt.Sprintf("recent messages to show on start, 0 for none (default %d, or 0 with --json)", defaultBacklog))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *channel == "" {
		return errors.New("--channel is required")
	}
	if *colorMode != "auto" && *colorMode != "always" && *colorMode != "never" {
		return errors.New("--color must be auto, always, or never")
	}
	if *backlog < 0 {
		*backlog = defaultBacklog
		if opt.json {
			*backlog = 0
		}
	}
	if *backlog > 1000 {
		return errors.New("--backlog must be between 0 and 1000")
	}
	if opt.nick == "" {
		opt.nick = defaultQueryNick()
	}
	w := &watcher{
		opt: *opt, out: stdout, json: opt.json, backlog: *backlog,
		view:    newRenderer(useColor(*colorMode, stdout), outputWidth(*width, stdout), strings.Contains(*channel, ",")),
		encoder: json.NewEncoder(stdout), last: map[string]string{}, seen: map[string]struct{}{},
	}
	for _, item := range strings.Split(*channel, ",") {
		item = channelName(item)
		target := watchTarget{observe: item, history: item}
		if strings.HasPrefix(item, "@") {
			target.history = item[1:]
		}
		w.targets = append(w.targets, target)
	}
	return w.run(ctx)
}

// run keeps a session going until ctx ends. Failing before the first session is
// established is an error; after that, outages are retried with backoff.
func (w *watcher) run(ctx context.Context) error {
	backoff := watchMinBackoff
	established := false
	for ctx.Err() == nil {
		var up bool
		client, err := irc.DialContext(ctx, w.config())
		if err == nil {
			up, err = w.session(ctx, client, !established)
			client.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		if up {
			established, backoff = true, watchMinBackoff
		}
		if !established {
			return err
		}
		if err := w.outage(err); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, watchMaxBackoff)
	}
	return nil
}

func (w *watcher) config() irc.Config {
	cfg := clientConfig(w.opt)
	cfg.Ephemeral = true
	return cfg
}

// session runs one connection. up reports whether it got as far as streaming.
func (w *watcher) session(ctx context.Context, client *irc.Client, first bool) (up bool, err error) {
	stopClose := context.AfterFunc(ctx, func() { _ = client.Close() })
	defer stopClose()
	names := make([]string, len(w.targets))
	for i, target := range w.targets {
		names[i] = target.observe
	}
	// Subscribe before reading history so nothing can fall in the gap between them.
	if err := client.Observe(names...); err != nil {
		return false, err
	}
	if err := awaitObservation(ctx, client, len(names)); err != nil {
		return false, err
	}
	var pending []irc.Event
	var caught []*irc.HistoryEvent
	if client.Ephemeral() {
		if caught, err = w.catchUp(ctx, client, first, func(event irc.Event) { pending = append(pending, event) }); err != nil {
			return false, err
		}
	}
	if first {
		if err := w.write(w.banner()); err != nil {
			return false, err
		}
		if err := w.showTopics(ctx, client, func(event irc.Event) { pending = append(pending, event) }); err != nil {
			return false, err
		}
	}
	if w.down {
		w.down = false
		if err := w.show("reconnected", true); err != nil {
			return true, err
		}
		if !client.Ephemeral() {
			if err := w.show("this server cannot replay history; messages sent while disconnected are not shown", false); err != nil {
				return true, err
			}
		}
	}
	for _, message := range caught {
		w.view.reserve(message.From) // fix the nick column before printing anything
	}
	for _, message := range caught {
		if err := w.emit(&irc.MessageEvent{Type: "message", ID: message.ID, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}); err != nil {
			return true, err
		}
	}
	if first && len(caught) > 0 && !w.json {
		if err := w.write(w.view.rule("live")); err != nil {
			return true, err
		}
	}
	for _, event := range pending {
		if err := w.handle(event); err != nil {
			return true, err
		}
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return true, errors.New("server closed the connection")
			}
			if err := w.handle(event); err != nil {
				return true, err
			}
		case <-ctx.Done():
			return true, nil
		}
	}
}

// catchUp returns the messages to show before going live: on the first session
// the newest --backlog of each target, afterwards everything since the last
// message shown. Targets with nothing to resume get their cursor anchored so a
// later outage can be recovered too.
func (w *watcher) catchUp(ctx context.Context, client *irc.Client, first bool, other func(irc.Event)) ([]*irc.HistoryEvent, error) {
	var all []*irc.HistoryEvent
	for _, target := range w.targets {
		key := targetKey(target.history)
		after := w.last[key]
		limit := 1000
		if first {
			limit = max(w.backlog, 1)
		}
		for page := 0; page < maxCheckPages; page++ {
			page, err := fetchHistory(ctx, client, target.history, after, limit, other)
			if err != nil {
				return nil, err
			}
			messages, status := page.messages, page.status
			if status == "expired" {
				if err := w.show("some messages from while this watcher was disconnected are no longer available", false); err != nil {
					return nil, err
				}
			}
			if len(messages) > 0 {
				after = messages[len(messages)-1].ID
			}
			if first {
				// Anchor the cursor on the newest message, but show only the backlog.
				if len(messages) > 0 {
					w.last[key] = after
				}
				if keep := min(w.backlog, len(messages)); keep < len(messages) {
					messages = messages[len(messages)-keep:]
				}
			}
			all = append(all, messages...)
			if status != "more" || first {
				break
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].Timestamp.Equal(all[j].Timestamp) {
			return all[i].Timestamp.Before(all[j].Timestamp)
		}
		return all[i].Seq < all[j].Seq
	})
	return all, nil
}

func targetKey(target string) string {
	if isChannel(target) {
		return target
	}
	return strings.ToLower(target)
}

func (w *watcher) handle(event irc.Event) error {
	if !isWatchEvent(event) {
		return nil
	}
	if message, ok := event.(*irc.MessageEvent); ok {
		return w.emit(message)
	}
	if w.json {
		return w.encoder.Encode(event)
	}
	return w.write(w.view.render(event))
}

// emit shows a message unless it has been shown already, and advances the cursor.
func (w *watcher) emit(message *irc.MessageEvent) error {
	if message.ID != "" {
		if _, dup := w.seen[message.ID]; dup {
			return nil
		}
		w.seen[message.ID] = struct{}{}
		w.seenOrder = append(w.seenOrder, message.ID)
		if len(w.seenOrder) > seenLimit {
			delete(w.seen, w.seenOrder[0])
			w.seenOrder = w.seenOrder[1:]
		}
		w.last[targetKey(message.Target)] = message.ID
	}
	if w.json {
		return w.encoder.Encode(message)
	}
	return w.write(w.view.render(message))
}

func (w *watcher) write(text string) error {
	_, err := io.WriteString(w.out, text)
	return err
}

func (w *watcher) banner() string {
	if w.json {
		return ""
	}
	names := make([]string, len(w.targets))
	for i, target := range w.targets {
		names[i] = target.observe
	}
	return w.view.banner(names)
}

// showTopics prints each watched channel's header once, at startup.
func (w *watcher) showTopics(ctx context.Context, client *irc.Client, other func(irc.Event)) error {
	if !client.Supports("TOPIC") {
		return nil
	}
	for _, target := range w.targets {
		if !isChannel(target.observe) {
			continue
		}
		text, err := fetchTopic(ctx, client, target.observe, other)
		if err != nil {
			return err
		}
		if text == "" {
			continue
		}
		if err := w.handle(&irc.TopicEvent{Type: "topic", Channel: target.observe, Topic: text}); err != nil {
			return err
		}
	}
	return nil
}

// show prints a status line in human mode; good marks recovery rather than trouble.
func (w *watcher) show(text string, good bool) error {
	if w.json {
		if good {
			return w.encoder.Encode(&irc.ConnectionEvent{Type: "connection", Connected: true})
		}
		return nil
	}
	return w.write(w.view.info(text, good))
}

// outage reports the first failure of a run of them; retries stay quiet.
func (w *watcher) outage(cause error) error {
	if w.down {
		return nil
	}
	w.down = true
	reason := "disconnected"
	if cause != nil {
		reason = cause.Error()
	}
	if w.json {
		return w.encoder.Encode(&irc.ConnectionEvent{Type: "connection", Connected: false, Error: reason})
	}
	return w.write(w.view.info("connection lost ("+reason+"); retrying", false))
}

// useColor decides whether to emit ANSI styling: only for a real terminal
// unless forced, and never when NO_COLOR is set or the terminal is "dumb".
func useColor(mode string, out io.Writer) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	file, ok := out.(*os.File)
	return ok && isTerminal(file) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// outputWidth picks the wrap width: --width, else the terminal, else $COLUMNS,
// capped so very wide terminals still produce readable lines.
func outputWidth(requested int, out io.Writer) int {
	width := requested
	if width <= 0 {
		if file, ok := out.(*os.File); ok {
			width = terminalWidth(file)
		}
	}
	if width <= 0 {
		width, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if width <= 0 {
		width = 100
	}
	if requested <= 0 {
		width = min(width-1, 120)
	}
	return width
}

// waitForObservation waits until the server has acknowledged every target in a
// comma-separated list; the server confirms each one separately.
func waitForObservation(ctx context.Context, client *irc.Client, targets string) error {
	return awaitObservation(ctx, client, len(strings.Split(targets, ",")))
}

func isWatchEvent(event irc.Event) bool {
	switch event.(type) {
	case *irc.MessageEvent, *irc.JoinEvent, *irc.PartEvent, *irc.QuitEvent, *irc.ConnectionEvent, *irc.TopicEvent:
		return true
	default:
		return false
	}
}
