package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const (
	observeChunk    = 16 // the server accepts at most this many targets per OBSERVE
	maxObserved     = 44 // leave room for 16 threads, inbox and DM audit
	inboxBacklog    = 50
	needNewerServer = "airc ui needs a current aircd (it has no channel directory); restart the server from a current build"
)

// uiBackend owns the connection for the UI. It discovers channels, subscribes,
// loads history, streams, and reconnects with catch-up, reporting everything to
// the model as messages.
type uiBackend struct {
	opt     options
	nick    string
	initial []string
	backlog int
	out     chan<- any
	cmds    <-chan uiCmd

	last     map[string]string // newest message ID forwarded per channel, for catch-up
	observed map[string]bool
	names    map[string][]string
	pending  []irc.ChannelInfo
	fresh    []string // channels discovered after startup, still to subscribe to
	queries  map[string]uiQuery
	threads  map[string]bool
	notifier *desktopNotifier
}

func (b *uiBackend) emit(ctx context.Context, msg any) {
	select {
	case b.out <- msg:
	case <-ctx.Done():
	}
}

func (b *uiBackend) inboxKey() string { return "@" + b.nick }

func (b *uiBackend) run(ctx context.Context) {
	b.last = map[string]string{}
	b.threads = map[string]bool{}
	retry := newBackoff(watchMinBackoff, watchMaxBackoff, false)
	established := false
	for ctx.Err() == nil {
		var up bool
		cfg, err := dialConfig(b.opt)
		if err != nil {
			b.emit(ctx, fatalIn{err})
			return
		}
		cfg.Ephemeral = true
		client, err := irc.DialContext(ctx, cfg)
		if err == nil {
			up, err = b.session(ctx, client, !established)
			client.Close()
		}
		if ctx.Err() != nil {
			return
		}
		if up {
			established = true
			retry.reset()
		}
		if !established {
			b.emit(ctx, fatalIn{err})
			return
		}
		offline := false
		reason := "disconnected"
		if err != nil {
			reason = err.Error()
		}
		b.emit(ctx, statusIn{text: "connection lost (" + reason + "); retrying", isError: true, connected: &offline})
		if !retry.wait(ctx) {
			return
		}
	}
}

func (b *uiBackend) session(ctx context.Context, client *irc.Client, first bool) (bool, error) {
	defer closeOnCancel(ctx, client)()
	if !client.Ephemeral() || !client.Supports("CHANNELS") {
		return false, errors.New(needNewerServer)
	}
	b.observed, b.names, b.pending, b.fresh = map[string]bool{}, map[string][]string{}, nil, nil
	translate := func(event irc.Event) { b.translate(ctx, event) }
	audit := client.Supports("DM_AUDIT")
	b.emit(ctx, auditIn{available: audit})

	list, err := fetchChannels(client, translate)
	if err != nil {
		return false, err
	}
	b.emit(ctx, channelsIn{list})
	names := append([]string{}, b.initial...)
	for _, info := range list {
		names = append(names, info.Name)
	}
	names = uniqueFirst(names, maxObserved)

	targets := append([]string{b.inboxKey()}, names...)
	if audit {
		targets = append(targets, irc.AllDirectMessages)
	}
	if err := b.subscribe(ctx, client, targets, translate); err != nil {
		return false, err
	}
	for _, name := range names {
		if err := b.load(ctx, client, name, name, first, translate); err != nil {
			return false, err
		}
	}
	inbox := b.inboxKey()
	if !client.Supports("MENTIONS") {
		inbox = b.nick // older servers only know direct messages
	}
	if err := b.load(ctx, client, inbox, b.inboxKey(), first, translate); err != nil {
		return false, err
	}
	if audit {
		if err := b.load(ctx, client, irc.AllDirectMessages, irc.AllDirectMessages, first, translate); err != nil {
			return false, err
		}
	}
	online := true
	for id := range b.threads {
		if err := b.subscribe(ctx, client, []string{"thread:" + id}, translate); err != nil {
			return false, err
		}
		if q, ok := b.queries["thread:"+id]; ok && q.Status != "more" {
			if err := b.pageQuery(ctx, client, uiCmd{kind: "query-next", target: "thread:" + id}, translate); err != nil {
				return false, err
			}
		}
	}
	b.emit(ctx, statusIn{connected: &online})
	if !first {
		b.emit(ctx, statusIn{text: "reconnected"})
	}

	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return true, errors.New("server closed the connection")
			}
			translate(event)
			if len(b.fresh) > 0 {
				fresh := b.fresh
				b.fresh = nil
				if err := b.attach(ctx, client, fresh, translate); err != nil {
					return true, err
				}
			}
		case cmd := <-b.cmds:
			if err := b.run1(ctx, client, cmd, translate); err != nil {
				return true, err
			}
		case <-ctx.Done():
			return true, nil
		}
	}
}

func (b *uiBackend) run1(ctx context.Context, client *irc.Client, cmd uiCmd, translate func(irc.Event)) error {
	if cmd.kind == "send" || cmd.kind == "reply" || cmd.kind == "retry-send" {
		acceptance, id, err := b.sendConfirmed(ctx, client, cmd, translate)
		b.emit(ctx, deliveryIn{cmd: cmd, err: err, uncertainID: id, acceptance: acceptance})
		if id != "" {
			return err
		}
		if err != nil {
			b.emit(ctx, statusIn{text: err.Error(), isError: true})
		}
		return nil
	}

	cmdCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if handled, err := b.runChatUI(cmdCtx, client, cmd, translate); handled {
		if confirmedUICmd(cmd) {
			b.emit(ctx, deliveryIn{cmd: cmd, err: err})
		}
		if err != nil {
			b.emit(ctx, statusIn{text: err.Error(), isError: true})
			if cmdCtx.Err() != nil || cmd.kind == "context" {
				return err
			}
		}
		return nil
	}
	var err error
	switch cmd.kind {
	case "room-op", "room-deop", "room-kick":
		nick, reason, _ := strings.Cut(cmd.text, " ")
		if cmd.kind == "room-kick" {
			err = client.Kick(cmd.target, nick, reason)
		} else {
			err = client.SetOperator(cmd.target, nick, cmd.kind == "room-op")
		}
	case "away":
		state := "away"
		if cmd.text == "" {
			state = "clear"
		}
		err = client.SetPresence(state, cmd.text, time.Hour)
	case "topic":
		err = client.SetTopic(cmd.target, cmd.text)
	case "names":
		err = client.Names(cmd.target)
	case "channels":
		err = client.Channels()
	case "observe":
		return b.attach(ctx, client, []string{cmd.target}, translate)
	}
	if err != nil {
		b.emit(ctx, statusIn{text: err.Error(), isError: true})
	}
	return nil
}

// subscribe observes targets in groups the server accepts.
func (b *uiBackend) subscribe(ctx context.Context, client *irc.Client, targets []string, translate func(irc.Event)) error {
	for len(targets) > 0 {
		chunk := targets[:min(observeChunk, len(targets))]
		targets = targets[len(chunk):]
		if err := client.Observe(chunk...); err != nil {
			return err
		}
		if err := awaitObservationWith(ctx, client, len(chunk), translate, false); err != nil {
			return err
		}
		for _, name := range chunk {
			b.observed[name] = true
		}
	}
	return nil
}

// attach subscribes to channels that appeared after startup and loads their history.
func (b *uiBackend) attach(ctx context.Context, client *irc.Client, channels []string, translate func(irc.Event)) error {
	var todo []string
	for _, channel := range channels {
		if !b.observed[channel] && len(b.observed) < maxObserved {
			todo = append(todo, channel)
		}
	}
	if err := b.subscribe(ctx, client, todo, translate); err != nil {
		b.emit(ctx, statusIn{text: err.Error(), isError: true})
		return nil
	}
	for _, channel := range todo {
		if err := b.load(ctx, client, channel, channel, true, translate); err != nil {
			return err
		}
	}
	return nil
}

// load reads a target's history into the model: the newest backlog the first
// time, and everything since the last message seen after a reconnect. Catch-up
// messages count as new (unread); the first load does not.
func (b *uiBackend) load(ctx context.Context, client *irc.Client, target, key string, first bool, translate func(irc.Event)) error {
	after := b.last[key]
	limit := 1000
	initial := after == ""
	if initial && !first && key == irc.AllDirectMessages && client.Supports("HISTORY_START") {
		after, initial = "*", false
	}
	if initial {
		limit = b.backlog
		if key == b.inboxKey() {
			limit = inboxBacklog
		}
		if limit == 0 {
			return nil
		}
	}
	for page := 0; page < maxCheckPages; page++ {
		page, err := fetchHistory(ctx, client, target, after, limit, translate)
		if err != nil {
			return err
		}
		messages, status := page.messages, page.status
		if status == "expired" {
			b.emit(ctx, statusIn{text: "some messages from while the UI was disconnected are no longer available", isError: true})
			if key == irc.AllDirectMessages && client.Supports("HISTORY_START") {
				page, err = fetchHistory(ctx, client, target, "*", limit, translate)
				if err != nil {
					return err
				}
				messages, status = page.messages, page.status
			}
		}
		for _, m := range messages {
			event := &irc.MessageEvent{ChatMetadata: m.ChatMetadata, Type: "message", ID: m.ID, ReplyTo: m.ReplyTo, ThreadID: m.ThreadID, Reaction: m.Reaction, From: m.From, Target: m.Target, Message: m.Message, Timestamp: m.Timestamp}
			b.emit(ctx, msgIn{event: event, history: first && initial})
			after = m.ID
			b.last[key] = m.ID
		}
		if status != "more" {
			if page.cursor != "" {
				b.last[key] = page.cursor
			}
			break
		}
	}
	return nil
}

func (b *uiBackend) translate(ctx context.Context, event irc.Event) {
	switch e := event.(type) {
	case *irc.MessageEvent:
		if b.notifier != nil && !strings.EqualFold(e.From, b.nick) && addressedTo(b.nick, e.Target, e.Message) {
			if err := b.notifier.notify(ctx, e.From+" in "+e.Target, e.Message); err != nil {
				b.emit(ctx, statusIn{text: "Notification failed: " + err.Error(), isError: true})
			}
		}
		b.emit(ctx, msgIn{event: e})
		if isChannel(e.Target) {
			b.last[e.Target] = e.ID
		} else {
			b.last[irc.AllDirectMessages] = e.ID
			if strings.EqualFold(e.Target, b.nick) {
				b.last[b.inboxKey()] = e.ID
			}
		}
	case *irc.SendReceiptEvent:
		b.emit(ctx, msgIn{event: e.MessageEvent()})
		if !isChannel(e.Target) {
			text := "sent to " + e.Target
			if e.Queued {
				text = e.Target + " is not connected; queued for their next check"
			}
			b.emit(ctx, statusIn{text: text})
		}
	case *irc.TopicEvent:
		b.emit(ctx, topicIn{channel: e.Channel, topic: e.Topic, by: e.SetBy})
	case *irc.JoinEvent, *irc.PartEvent, *irc.KickEvent:
		b.emit(ctx, msgIn{event: e})
	case *irc.SignalEvent:
		if e.Action == "cancel" || time.Now().Before(e.ExpiresAt) {
			b.emit(ctx, signalIn{entry: e.ChatEntry})
		}
	case *irc.ChannelEvent:
		b.pending = append(b.pending, e.Channel)
	case *irc.EndOfChannelsEvent:
		b.emit(ctx, channelsIn{b.pending})
		for _, info := range b.pending {
			if !b.observed[info.Name] {
				b.fresh = append(b.fresh, info.Name)
			}
		}
		b.pending = nil
	case *irc.RawEvent:
		switch e.Command {
		case "MODE":
			b.emit(ctx, statusIn{text: strings.Join(e.Params, " ")})
		case "353":
			if len(e.Params) >= 3 {
				for _, nick := range strings.Fields(e.Trailing) {
					b.names[e.Params[2]] = append(b.names[e.Params[2]], strings.TrimLeft(nick, "@+"))
				}
			}
		case "366":
			if len(e.Params) >= 2 {
				b.emit(ctx, namesIn{channel: e.Params[1], nicks: b.names[e.Params[1]]})
				delete(b.names, e.Params[1])
			}
		}
		if err := serverError(e); err != nil {
			b.emit(ctx, statusIn{text: err.Error(), isError: true})
		}
	}
}

// fetchChannels asks for every channel the server knows. other sees unrelated events.
func fetchChannels(client *irc.Client, other func(irc.Event)) ([]irc.ChannelInfo, error) {
	if err := client.Channels(); err != nil {
		return nil, err
	}
	var list []irc.ChannelInfo
	err := awaitEvent(context.Background(), client, requestTimeout, "listing channels", "timed out waiting for the channel list", func(event irc.Event) (bool, error) {
		switch e := event.(type) {
		case *irc.ChannelEvent:
			list = append(list, e.Channel)
		case *irc.EndOfChannelsEvent:
			return true, nil
		default:
			if other != nil {
				other(event)
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return list, nil
}

// uniqueFirst keeps the first occurrence of each name, up to limit names.
func uniqueFirst(names []string, limit int) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		if name != "" && !seen[name] && len(out) < limit {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}
