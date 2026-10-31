package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

type queryIn struct {
	name       string
	page       *uiQuery
	appendPage bool
	thread     bool
	messages   []*irc.HistoryEvent
}
type signalIn struct{ entry irc.ChatEntry }

func (m *uiModel) submitChat(name, rest string, b *uiBuffer) ([]uiCmd, bool) {
	fields := strings.Fields(rest)
	need := func(n int) bool {
		if len(fields) < n {
			m.setStatus("Missing arguments for "+name+"; try /help", true)
			return false
		}
		return true
	}
	// tail is rest after its first field: a step's note, a question's text.
	tail := ""
	if len(fields) > 0 {
		tail = strings.TrimSpace(rest[len(fields[0]):])
	}
	// build shapes a structured action's request, or says why it cannot.
	build := func(in chatInput) ([]uiCmd, bool) {
		r, err := in.request()
		if err != nil {
			m.setStatus(err.Error(), true)
			return nil, true
		}
		return []uiCmd{{kind: "chat", request: &r}}, true
	}
	if len(fields) > 0 && fields[0] == "last" && b != nil {
		for _, v := range slices.Backward(b.items) {
			if event, ok := v.(*irc.MessageEvent); ok && event.ID != "" {
				fields[0] = event.ID
				break
			}
		}
	}
	request := irc.ChatRequest{}
	switch name {
	case "/context", "/thread", "/follow", "/unfollow", "/paste":
		if !need(1) {
			return nil, true
		}
		return []uiCmd{{kind: name[1:], target: fields[0]}}, true
	case "/reply", "/react":
		if !need(2) {
			return nil, true
		}
		return []uiCmd{{kind: name[1:], target: fields[0], text: strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])}}, true
	case "/search":
		if !need(1) || b == nil {
			return nil, true
		}
		return []uiCmd{{kind: "search", target: queryRoom(b), text: rest}}, true
	case "/run":
		room := queryRoom(b)
		if !need(1) {
			return nil, true
		}
		if !protocol.IsChannel(room) || !protocol.ValidRunID(fields[0]) {
			m.setStatus("/run ID works in a channel; an ID is 1-64 letters, digits, '.', '_' or '-'", true)
			return nil, true
		}
		return []uiCmd{{kind: "run", target: room, text: fields[0]}}, true
	case "/charter":
		room := queryRoom(b)
		if !protocol.IsChannel(room) {
			m.setStatus("/charter works in a channel", true)
			return nil, true
		}
		return []uiCmd{{kind: "charter", target: room, text: "open"}}, true
	case "/correct", "/retract", "/prepare", "/waiting", "/cancel":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID, request.Text = name[1:], fields[0], tail
		if name == "/prepare" {
			request.Seconds = 120
		}
	case "/pin", "/unpin":
		if !need(1) {
			return nil, true
		}
		return build(pinInput{Step: name[1:], ID: fields[0]})
	case "/vote":
		if !need(2) {
			return nil, true
		}
		choice, err := strconv.Atoi(fields[1])
		if err != nil {
			m.setStatus("/vote ID CHOICE_NUMBER", true)
			return nil, true
		}
		return build(pollInput{Step: "vote", ID: fields[0], Choice: choice})
	case "/results", "/close-poll":
		if !need(1) {
			return nil, true
		}
		return build(pollInput{Step: strings.TrimSuffix(name[1:], "-poll"), ID: fields[0]})
	case "/me", "/pins", "/poll":
		if b == nil || b.kind != bufChannel {
			m.setStatus("This command requires a room", true)
			return nil, true
		}
		switch name {
		case "/me":
			if !need(1) {
				return nil, true
			}
			request.Action, request.Target, request.Text = "action", b.name, rest
		case "/pins":
			return build(pinInput{Step: "list", Channel: b.name})
		case "/poll":
			parts := splitPipes(rest)
			if len(parts) < 3 {
				m.setStatus("/poll Question | option 1 | option 2", true)
				return nil, true
			}
			return build(pollInput{Channel: b.name, Question: parts[0], Options: parts[1:], Seconds: 3600})
		}
	case "/handoff":
		if !need(1) {
			return nil, true
		}
		in := handoffInput{Message: rest}
		switch {
		case b != nil && b.kind == bufChannel:
			in.Channel = b.name
			if nick, ok := strings.CutPrefix(fields[0], "@"); ok && len(fields) > 1 {
				in.To, in.Message = nick, tail
			}
		case b != nil && b.kind == bufQuery && protocol.ValidNick(b.name):
			in.To = b.name
		default:
			m.setStatus("/handoff needs a room or a direct conversation", true)
			return nil, true
		}
		return build(in)
	case "/accept", "/blocked", "/decline", "/done", "/withdraw":
		if !need(1) {
			return nil, true
		}
		return build(handoffInput{Step: name[1:], ID: fields[0], Message: tail})
	case "/handoffs":
		request.Action = "handoffs"
	case "/board":
		room := queryRoom(b)
		if len(fields) > 0 {
			room = channelName(fields[0])
		}
		if !protocol.IsChannel(room) {
			m.setStatus("/board needs a room: /board #room", true)
			return nil, true
		}
		return []uiCmd{{kind: "board", target: room}}, true
	case "/decide":
		if !need(1) {
			return nil, true
		}
		in := decisionInput{}
		switch {
		case b != nil && b.kind == bufChannel:
			nick, ok := strings.CutPrefix(fields[0], "@")
			if !ok || len(fields) < 2 {
				m.setStatus("/decide @nick question | option | option", true)
				return nil, true
			}
			in.Channel, in.From, in.Message = b.name, nick, tail
		case b != nil && b.kind == bufQuery && protocol.ValidNick(b.name):
			in.To, in.Message = b.name, rest
		default:
			m.setStatus("/decide needs a room or a direct conversation", true)
			return nil, true
		}
		parts := splitPipes(in.Message)
		in.Message = parts[0]
		if len(parts) > 1 {
			in.Options = parts[1:]
		}
		return build(in)
	case "/answer":
		if !need(1) {
			return nil, true
		}
		in := decisionInput{Step: "answer", ID: fields[0], Message: tail}
		if len(fields) > 1 {
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
				in.Choice, in.Message = n, strings.TrimSpace(tail[len(fields[1]):])
			}
		}
		return build(in)
	case "/withdraw-decision":
		if !need(1) {
			return nil, true
		}
		return build(decisionInput{Step: "withdraw", ID: fields[0], Message: tail})
	case "/decisions":
		request.Action = "decisions"
	case "/status":
		// /status task | state | note posts from this room; /status alone
		// lists the room's latest statuses in the status line.
		if b == nil || b.kind != bufChannel {
			m.setStatus("/status works in a room", true)
			return nil, true
		}
		in := statusInput{Channel: b.name}
		if rest != "" {
			parts := splitPipes(rest)
			if len(parts) < 2 {
				m.setStatus("/status task | working|blocked|done | note", true)
				return nil, true
			}
			in.Task, in.State = parts[0], parts[1]
			if len(parts) > 2 {
				in.Message = strings.Join(parts[2:], " | ")
			}
		}
		return build(in)
	case "/resolve":
		if !need(1) {
			return nil, true
		}
		return build(resolveInput{ID: fields[0], Message: tail})
	case "/op", "/deop", "/kick":
		if b == nil || b.kind != bufChannel || !need(1) {
			m.setStatus("Use "+name+" nick [reason] in a channel", true)
			return nil, true
		}
		return []uiCmd{{kind: "room-" + name[1:], target: b.name, text: rest}}, true
	case "/away":
		return []uiCmd{{kind: "away", text: rest}}, true
	case "/mute", "/unmute", "/ban", "/unban", "/disconnect", "/bans":
		if name != "/bans" && !need(1) {
			return nil, true
		}
		action := name[1:]
		if name == "/disconnect" {
			action = "kick"
		}
		return []uiCmd{{kind: "admin", target: action, text: rest}}, true
	default:
		return nil, false
	}
	return []uiCmd{{kind: "chat", request: &request}}, true
}

func (b *uiBackend) runChatUI(ctx context.Context, c *irc.Client, cmd uiCmd, translate func(irc.Event)) (bool, error) {
	if (cmd.kind == "query-next" || cmd.kind == "query-first") && strings.HasPrefix(cmd.target, "board:") {
		// A board has no pages; /next and /first redraw it.
		cmd = uiCmd{kind: "board", target: strings.TrimPrefix(cmd.target, "board:"), text: "refresh"}
	}
	if (cmd.kind == "query-next" || cmd.kind == "query-first") && strings.HasPrefix(cmd.target, "paste:") {
		// A paste view holds the whole paste; its pages are the model's.
		b.emit(ctx, pastePageIn{name: cmd.target, first: cmd.kind == "query-first"})
		return true, nil
	}
	switch cmd.kind {
	case "paste":
		meta, content, err := irc.FetchPaste(ctx, c, cmd.target, translate)
		if err != nil {
			return true, err
		}
		b.emit(ctx, pasteIn{entry: meta, content: content})
		return true, nil
	case "board":
		return true, b.refreshBoard(ctx, c, cmd.target, cmd.text == "refresh", translate)
	case "close-query":
		delete(b.threads, strings.TrimPrefix(cmd.target, "thread:"))
		delete(b.queries, cmd.target)
		delete(b.observed, cmd.target)
		if !strings.HasPrefix(cmd.target, "thread:") {
			return true, nil
		}
		return true, c.Raw("UNOBSERVE " + cmd.target)
	case "context":
		return true, b.openContext(ctx, c, cmd.target, translate)
	case "charter":
		// Read for /charter (text "open"), on first showing a room, or
		// after a reconnect; the live signal only notes a change.
		entries, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "room", Target: cmd.target, Seconds: -1, Limit: -1}, translate)
		if err != nil {
			return true, err
		}
		charter := ""
		for _, e := range entries {
			charter = e.Charter
		}
		b.emit(ctx, charterIn{channel: cmd.target, charter: charter, open: cmd.text == "open"})
		return true, nil
	case "react":
		return true, c.React(cmd.target, cmd.text)
	case "chat":
		entries, err := irc.RequestChat(ctx, c, *cmd.request, translate)
		if err == nil && (cmd.request.Action == "handoffs" || cmd.request.Action == "decisions" || cmd.request.Action == "statuses") {
			// One status line holds the whole list.
			listed := []string{}
			for _, e := range entries {
				text := handoffText(e)
				switch e.Action {
				case "decision":
					text = decisionText(e)
				case "status":
					text = statusText(e)
				}
				listed = append(listed, strings.SplitN(text, "\n", 2)[0])
			}
			if cmd.request.Action == "decisions" {
				b.emit(ctx, decisionsIn{entries: entries})
			}
			b.emit(ctx, statusIn{text: cmp.Or(strings.Join(listed, "; "), "No "+cmd.request.Action)})
			return true, nil
		}
		if err == nil {
			for _, e := range entries {
				text := chatEntryText(e)
				switch e.Action {
				case "typing", "thinking":
					continue
				case "status":
					text = strings.SplitN(statusText(e), "\n", 2)[0]
				case "resolve":
					text = resolveText(e)
				}
				b.emit(ctx, statusIn{text: text})
			}
		}
		return true, err
	case "thread", "search", "run", "query-next", "query-first":
		return true, b.pageQuery(ctx, c, cmd, translate)
	case "follow", "unfollow":
		args := []string{cmd.target, "--nick", b.nick, "--addr", b.opt.addr}
		if b.opt.unix != "" {
			args = append(args, "--unix", b.opt.unix)
		}
		if b.opt.identityFile != "" {
			args = append(args, "--identity", b.opt.identityFile)
		}
		args = append(args, transportArgs(b.opt)...)
		var output bytes.Buffer
		err := runSessionCommand(cmd.kind, args, nil, &output, &output)
		if err == nil {
			b.emit(ctx, statusIn{text: cmd.kind + " " + cmd.target})
		}
		return true, err
	case "admin":
		path, err := defaultAdminTokenFile()
		if err != nil {
			return true, err
		}
		token, err := admin.ReadToken(path)
		if err != nil {
			return true, err
		}
		if err := c.AuthenticateAdmin(token); err != nil {
			return true, err
		}
		fields := strings.Fields(cmd.text)
		r := irc.AdminRequest{Action: cmd.target, Scope: "*"}
		if r.Action == "bans" {
			r.Action = "list"
		} else {
			if len(fields) == 0 {
				return true, errors.New("nickname required")
			}
			r.Nick = fields[0]
			if len(fields) > 1 {
				r.Reason = strings.Join(fields[1:], " ")
			}
		}
		if err := c.Moderate(r); err != nil {
			return true, err
		}
		err = awaitReply(ctx, c, "disconnected during moderation", func(event irc.Event) (bool, error) {
			switch e := event.(type) {
			case *irc.AdminEvent:
				b.emit(ctx, statusIn{text: adminText(e.AdminResult)})
			case *irc.EndOfAdminEvent:
				return true, nil
			default:
				translate(event)
			}
			return false, nil
		})
		return true, err
	}
	return false, nil
}

// refreshBoard reads room's handoffs and hands them to the board view; a
// refresh redraws an open board without switching to it.
func (b *uiBackend) refreshBoard(ctx context.Context, c *irc.Client, room string, refresh bool, translate func(irc.Event)) error {
	entries, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "handoffs", Target: room}, translate)
	if err != nil {
		return err
	}
	if b.queries == nil {
		b.queries = map[string]uiQuery{}
	}
	b.queries[boardName(room)] = uiQuery{Kind: "board", Target: room}
	b.emit(ctx, boardIn{room: room, entries: entries, refresh: refresh})
	return nil
}

// showCharter keeps a room's charter as the backend read it. For /charter it
// opens the charter as a read-only view, line by line, or says in the status
// line that the room has none; /close returns. A read made on showing the
// room or after a reconnect notes a charter the UI had not seen, or one
// that changed while it was away, and redraws an open view in place.
func (m *uiModel) showCharter(c charterIn) {
	room := m.ensureChannel(c.channel)
	known, previous := room.charterKnown, room.charter
	room.charter, room.charterKnown = c.charter, true
	name := "charter:" + c.channel
	view := m.find(name)
	if !c.open {
		switch {
		case !known && c.charter != "":
			m.setStatus(c.channel+" has a charter; /charter shows it", false)
		case known && c.charter != previous:
			m.setStatus("the charter of "+c.channel+" changed while the UI was away; /charter shows it", false)
		}
		if view != nil {
			renderCharter(view, c)
		}
		return
	}
	if c.charter == "" {
		m.setStatus(c.channel+" has no charter", false)
		if view != nil {
			renderCharter(view, c) // an open view no longer shows a cleared charter
		}
		return
	}
	if view == nil {
		view = newBuffer(name, bufHelp)
		view.returnTo = m.current
		m.buffers = append(m.buffers, view)
	}
	renderCharter(view, c)
	m.switchTo(name)
}

// renderCharter draws a charter view: the heading, then the charter line by
// line, or that the room has none.
func renderCharter(view *uiBuffer, c charterIn) {
	view.help = []helpLine{{head: "Charter of " + c.channel}}
	if c.charter == "" {
		view.help = append(view.help, helpLine{body: c.channel + " has no charter"})
		return
	}
	for line := range strings.SplitSeq(c.charter, "\n") {
		view.help = append(view.help, helpLine{body: line})
	}
	view.topic = "Charter of " + c.channel + " · /close returns to " + view.returnTo
	view.version++
}

func (m *uiModel) showQuery(q queryIn) {
	b := m.find(q.name)
	if b == nil {
		count := 0
		for _, candidate := range m.buffers {
			if candidate.kind == bufQuery {
				count++
			}
		}
		if count >= 16 {
			m.setStatus("Close a query view before opening another (limit 16)", true)
			return
		}
		b = newBuffer(q.name, bufQuery)
		b.returnTo = m.current
		m.buffers = append(m.buffers, b)
	}
	if !q.appendPage && (!q.thread || q.page != nil) {
		b.items, b.seen = nil, map[string]struct{}{}
		b.version++
	}
	for _, e := range q.messages {
		b.add(e.MessageEvent())
	}
	if q.page != nil {
		copy := *q.page
		b.query = &copy
		b.topic = copy.description()
	}
	if !q.appendPage {
		m.switchTo(q.name)
	}
}

func (m *uiModel) typingCommand(cmds []uiCmd) []uiCmd {
	b := m.cur()
	if b != nil && b.kind == bufChannel && m.now().Sub(m.lastTyping) >= 3*time.Second {
		m.lastTyping = m.now()
		cmds = append(cmds, uiCmd{kind: "chat", request: &irc.ChatRequest{Action: "typing", Target: b.name, Seconds: 10}})
	}
	return cmds
}
