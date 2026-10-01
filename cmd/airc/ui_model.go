package main

import (
	"sort"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

const (
	bufferLimit  = 500
	refreshEvery = 15 * time.Second
	statusHold   = 6 * time.Second
)

type bufferKind int

const (
	bufChannel        bufferKind = iota
	bufInbox                     // direct messages and tags addressed to this user, from every channel
	bufDirectMessages            // human oversight of every direct message
	bufQuery
)

// uiBuffer is one view the user can switch to: a channel, or the inbox.
type uiBuffer struct {
	name    string
	kind    bufferKind
	items   []irc.Event // messages and notices, oldest first
	seen    map[string]struct{}
	unread  int
	mention bool // an unread item is addressed to this user
	scroll  int  // lines scrolled back from the newest
	topic   string
	live    []string // nicks of connected sessions, from NAMES
	info    irc.ChannelInfo
	version int // bumped on every change, to invalidate cached rendering

	cacheWidth, cacheVersion int
	cacheLines               []string
}

// Messages from the backend, the keyboard, and the terminal. The model handles
// them one at a time on a single goroutine, so it needs no locking.
type (
	msgIn struct {
		event   irc.Event
		history bool
	}
	topicIn struct{ channel, topic, by string }
	namesIn struct {
		channel string
		nicks   []string
	}
	channelsIn struct{ list []irc.ChannelInfo }
	auditIn    struct{ available bool }
	statusIn   struct {
		text      string
		isError   bool
		connected *bool
	}
	fatalIn  struct{ err error }
	keyIn    key
	resizeIn struct{ width, height int }
	tickIn   struct{}
)

// uiCmd is something the model asks the backend to do.
type uiCmd struct {
	kind         string // send, topic, names, channels, observe
	target, text string
	request      *irc.ChatRequest
}

type uiModel struct {
	nick          string
	buffers       []*uiBuffer
	current       string
	chosen        bool // the user has picked a view, so discovery must not move them
	input         []rune
	cursor        int
	width, height int
	connected     bool
	status        string
	statusError   bool
	statusUntil   time.Time
	lastRefresh   time.Time
	lastTyping    time.Time
	now           func() time.Time
}

func newUIModel(nick string, channels []string, now func() time.Time) *uiModel {
	m := &uiModel{nick: nick, now: now, width: 100, height: 30, lastRefresh: now()}
	m.buffers = []*uiBuffer{newBuffer("@"+nick, bufInbox), newBuffer(irc.AllDirectMessages, bufDirectMessages)}
	m.find(irc.AllDirectMessages).topic = "waiting for daemon capabilities"
	for _, channel := range channels {
		m.ensureChannel(channel)
	}
	m.current = m.firstName()
	return m
}

func newBuffer(name string, kind bufferKind) *uiBuffer {
	return &uiBuffer{name: name, kind: kind, seen: map[string]struct{}{}}
}

// firstName is the buffer to show at start: the first channel, else the inbox.
func (m *uiModel) firstName() string {
	if m.buffers[0].kind == bufChannel {
		return m.buffers[0].name
	}
	return m.inbox().name
}

func (m *uiModel) inbox() *uiBuffer { return m.find("@" + m.nick) }

func (m *uiModel) cur() *uiBuffer { return m.find(m.current) }

func (m *uiModel) find(name string) *uiBuffer {
	for _, b := range m.buffers {
		if b.name == name {
			return b
		}
	}
	return nil
}

// ensureChannel returns the buffer for a channel, creating it in alphabetical
// order, before the inbox and human DM view.
func (m *uiModel) ensureChannel(name string) *uiBuffer {
	if b := m.find(name); b != nil {
		return b
	}
	b := newBuffer(name, bufChannel)
	at := sort.Search(len(m.buffers), func(i int) bool {
		return m.buffers[i].kind != bufChannel || m.buffers[i].name > name
	})
	m.buffers = append(m.buffers, nil)
	copy(m.buffers[at+1:], m.buffers[at:])
	m.buffers[at] = b
	return b
}

func (b *uiBuffer) add(event irc.Event) bool {
	if message, ok := event.(*irc.MessageEvent); ok && message.ID != "" {
		if _, dup := b.seen[message.ID]; dup {
			return false
		}
		b.seen[message.ID] = struct{}{}
	}
	// Keep timestamp order even when a live message overtakes older history.
	at := len(b.items)
	if message, ok := event.(*irc.MessageEvent); ok {
		for at > 0 {
			previous, isMessage := b.items[at-1].(*irc.MessageEvent)
			if !isMessage || !previous.Timestamp.After(message.Timestamp) {
				break
			}
			at--
		}
	}
	b.items = append(b.items, nil)
	copy(b.items[at+1:], b.items[at:])
	b.items[at] = event
	if len(b.items) > bufferLimit {
		for _, evicted := range b.items[:len(b.items)-bufferLimit] {
			if message, ok := evicted.(*irc.MessageEvent); ok {
				delete(b.seen, message.ID)
			}
		}
		b.items = b.items[len(b.items)-bufferLimit:]
	}
	b.version++
	if b.scroll > 0 {
		b.scroll++ // keep the viewport still while the user reads older lines
	}
	return true
}

func (m *uiModel) notify(b *uiBuffer, addressed bool) {
	if b == m.cur() {
		return
	}
	b.unread++
	if addressed {
		b.mention = true
	}
}

// route files a message into its channel, and into the inbox when it is a direct
// message or tags this user.
func (m *uiModel) route(message *irc.MessageEvent, history bool) {
	for _, b := range m.buffers {
		if b.kind == bufQuery && strings.HasPrefix(b.name, "thread:") {
			id := strings.TrimPrefix(b.name, "thread:")
			if message.ID == id || message.ThreadID == id {
				if b.add(message) && !history {
					m.notify(b, false)
				}
			}
		}
		if message.Supersedes != "" {
			for _, item := range b.items {
				if original, ok := item.(*irc.MessageEvent); ok && original.ID == message.Supersedes {
					original.SupersededBy, original.Retracted = message.ID, message.Kind == "retract"
					b.version++
				}
			}
		}
	}
	own := strings.EqualFold(message.From, m.nick)
	addressed := !own && addressedTo(m.nick, message.Target, message.Message)
	if isChannel(message.Target) {
		if b := m.ensureChannel(message.Target); b.add(message) && !history && !own {
			m.notify(b, addressed)
		}
	}
	if (!isChannel(message.Target) && (own || strings.EqualFold(message.Target, m.nick))) || addressed {
		if inbox := m.inbox(); inbox.add(message) && !history && !own {
			m.notify(inbox, true)
		}
	}
	if !isChannel(message.Target) {
		if audit := m.find(irc.AllDirectMessages); audit.add(message) && !history && !own {
			m.notify(audit, false)
		}
	}
}

func (m *uiModel) setStatus(text string, isError bool) {
	m.status, m.statusError, m.statusUntil = text, isError, m.now().Add(statusHold)
}

func (m *uiModel) activeStatus() (string, bool) {
	if m.status != "" && m.now().Before(m.statusUntil) {
		return m.status, m.statusError
	}
	return "", false
}

func (m *uiModel) switchTo(name string) []uiCmd {
	b := m.find(name)
	if b == nil {
		return nil
	}
	m.current, m.chosen = name, true
	b.unread, b.mention, b.scroll = 0, false, 0
	if b.kind == bufChannel {
		return []uiCmd{{kind: "names", target: name}}
	}
	return nil
}

func (m *uiModel) step(delta int) []uiCmd {
	for i, b := range m.buffers {
		if b.name == m.current {
			return m.switchTo(m.buffers[(i+delta+len(m.buffers))%len(m.buffers)].name)
		}
	}
	return nil
}

// update applies one message and returns what the backend should do about it.
func (m *uiModel) update(msg any) (cmds []uiCmd, quit bool) {
	switch v := msg.(type) {
	case queryIn:
		m.showQuery(v)
	case signalIn:
		if v.entry.Action == "results" {
			m.setStatus(chatEntryText(v.entry), false)
			break
		}
		if v.entry.Action == "cancel" {
			if strings.HasPrefix(m.status, v.entry.From+" ") {
				m.status = ""
			}
			break
		}
		if m.now().Before(v.entry.ExpiresAt) {
			m.status, m.statusError, m.statusUntil = v.entry.From+" "+v.entry.Action+" "+v.entry.Text, false, v.entry.ExpiresAt
		}
	case msgIn:
		switch e := v.event.(type) {
		case *irc.MessageEvent:
			m.route(e, v.history)
		case *irc.JoinEvent:
			if b := m.find(e.Channel); b != nil && b.add(e) {
				m.notify(b, false)
			}
		case *irc.PartEvent:
			if b := m.find(e.Channel); b != nil && b.add(e) {
				m.notify(b, false)
			}
		}
	case topicIn:
		b := m.ensureChannel(v.channel)
		changed := b.topic != v.topic
		b.topic = v.topic
		if v.by != "" && changed { // a live change, not the header read at startup
			b.add(&irc.TopicEvent{Type: "topic", Channel: v.channel, Topic: v.topic, SetBy: v.by})
		}
		b.version++
	case namesIn:
		if b := m.find(v.channel); b != nil {
			b.live = v.nicks
		}
	case channelsIn:
		for _, info := range v.list {
			b := m.ensureChannel(info.Name)
			b.info, b.topic = info, info.Topic
		}
		// Started without a channel: open the first one rather than the read-only inbox.
		if !m.chosen && m.cur().kind == bufInbox && m.buffers[0].kind == bufChannel {
			m.current = m.buffers[0].name
			cmds = append(cmds, uiCmd{kind: "names", target: m.current})
		}
	case auditIn:
		b := m.find(irc.AllDirectMessages)
		b.topic = "all direct messages · human oversight · read-only"
		if !v.available {
			b.topic = "unavailable: daemon needs DM_AUDIT; upgrade/restart when safe"
		}
		b.version++
	case statusIn:
		if v.connected != nil {
			m.connected = *v.connected
		}
		if v.text != "" {
			m.setStatus(v.text, v.isError)
		}
	case resizeIn:
		m.width, m.height = v.width, v.height
	case tickIn:
		if m.now().Sub(m.lastRefresh) >= refreshEvery && m.connected {
			m.lastRefresh = m.now()
			cmds = append(cmds, uiCmd{kind: "channels"})
			if b := m.cur(); b != nil && b.kind == bufChannel {
				cmds = append(cmds, uiCmd{kind: "names", target: b.name})
			}
		}
	case keyIn:
		cmds, quit = m.handleKey(key(v))
		if key(v).kind == keyRune {
			cmds = m.typingCommand(cmds)
		}
		return cmds, quit
	}
	return cmds, false
}

func (m *uiModel) handleKey(k key) (cmds []uiCmd, quit bool) {
	page := max(m.height-4, 1)
	switch k.kind {
	case keyRune:
		m.input = append(m.input[:m.cursor], append([]rune{k.r}, m.input[m.cursor:]...)...)
		m.cursor++
	case keyBackspace:
		if m.cursor > 0 {
			m.input = append(m.input[:m.cursor-1], m.input[m.cursor:]...)
			m.cursor--
		}
	case keyDelete:
		if m.cursor < len(m.input) {
			m.input = append(m.input[:m.cursor], m.input[m.cursor+1:]...)
		}
	case keyLeft:
		m.cursor = max(m.cursor-1, 0)
	case keyRight:
		m.cursor = min(m.cursor+1, len(m.input))
	case keyHome:
		m.cursor = 0
	case keyEnd:
		m.cursor = len(m.input)
	case keyEsc:
		m.input, m.cursor = nil, 0
	case keyTab:
		return m.step(1), false
	case keyBackTab:
		return m.step(-1), false
	case keyUp:
		m.scrollBy(1)
	case keyDown:
		m.scrollBy(-1)
	case keyPgUp:
		m.scrollBy(page / 2)
	case keyPgDn:
		m.scrollBy(-page / 2)
	case keyEnter:
		return m.submit()
	case keyCtrl:
		switch k.r {
		case 'c':
			return nil, true
		case 'd':
			if len(m.input) == 0 {
				return nil, true
			}
		case 'n':
			return m.step(1), false
		case 'p':
			return m.step(-1), false
		case 'a':
			m.cursor = 0
		case 'e':
			m.cursor = len(m.input)
		case 'u':
			m.input, m.cursor = m.input[m.cursor:], 0
		case 'k':
			m.input = m.input[:m.cursor]
		case 'w':
			start := m.cursor
			for start > 0 && m.input[start-1] == ' ' {
				start--
			}
			for start > 0 && m.input[start-1] != ' ' {
				start--
			}
			m.input = append(m.input[:start], m.input[m.cursor:]...)
			m.cursor = start
		}
	}
	return nil, false
}

func (m *uiModel) scrollBy(lines int) {
	if b := m.cur(); b != nil {
		b.scroll = max(b.scroll+lines, 0)
	}
}

// submit runs the input line: a slash command, or a message to the current channel.
