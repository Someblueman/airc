package main

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
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
	case "/context", "/thread", "/follow", "/unfollow":
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
	case "/charter":
		room := queryRoom(b)
		if !protocol.IsChannel(room) {
			m.setStatus("/charter works in a channel", true)
			return nil, true
		}
		return []uiCmd{{kind: "charter", target: room}}, true
	case "/pin", "/unpin", "/correct", "/retract", "/prepare", "/waiting", "/cancel", "/vote", "/results", "/close-poll":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID = name[1:], fields[0]
		if len(fields) > 1 {
			request.Text = strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])
		}
		if name == "/prepare" {
			request.Seconds = 120
		}
		if name == "/vote" {
			if !need(2) {
				return nil, true
			}
			if _, err := fmt.Sscan(fields[1], &request.Choice); err != nil {
				m.setStatus("/vote ID CHOICE_NUMBER", true)
				return nil, true
			}
			request.Text = ""
		}
	case "/me", "/pins", "/poll":
		if b == nil || b.kind != bufChannel {
			m.setStatus("This command requires a room", true)
			return nil, true
		}
		request.Target = b.name
		if name == "/me" {
			if !need(1) {
				return nil, true
			}
			request.Action, request.Text = "action", rest
		}
		if name == "/pins" {
			request.Action = "pins"
		}
		if name == "/poll" {
			parts := strings.Split(rest, "|")
			if len(parts) < 3 {
				m.setStatus("/poll Question | option 1 | option 2", true)
				return nil, true
			}
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			request.Action, request.Text, request.Options, request.Seconds = "poll", parts[0], parts[1:], 3600
		}
	case "/handoff":
		if !need(1) {
			return nil, true
		}
		switch {
		case b != nil && b.kind == bufChannel:
			request.Target, request.Text = b.name, rest
			if nick, ok := strings.CutPrefix(fields[0], "@"); ok && len(fields) > 1 {
				request.For, request.Text = nick, strings.TrimSpace(rest[len(fields[0]):])
			}
		case b != nil && b.kind == bufQuery && protocol.ValidNick(b.name):
			request.Target, request.Text = b.name, rest
		default:
			m.setStatus("/handoff needs a room or a direct conversation", true)
			return nil, true
		}
		request.Action = "handoff"
	case "/accept", "/blocked", "/decline", "/done", "/withdraw":
		if !need(1) {
			return nil, true
		}
		if name == "/blocked" && len(fields) < 2 {
			m.setStatus("/blocked ID reason: say why the handoff is stuck", true)
			return nil, true
		}
		request.Action, request.ID = "handoff-"+name[1:], fields[0]
		request.Text = strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])
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
		switch {
		case b != nil && b.kind == bufChannel:
			nick, ok := strings.CutPrefix(fields[0], "@")
			if !ok || len(fields) < 2 {
				m.setStatus("/decide @nick question | option | option", true)
				return nil, true
			}
			request.Target, request.For, request.Text = b.name, nick, strings.TrimSpace(rest[len(fields[0]):])
		case b != nil && b.kind == bufQuery && protocol.ValidNick(b.name):
			request.Target, request.For, request.Text = b.name, b.name, rest
		default:
			m.setStatus("/decide needs a room or a direct conversation", true)
			return nil, true
		}
		parts := strings.Split(request.Text, "|")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		request.Action, request.Text = "decision", parts[0]
		if len(parts) > 1 {
			request.Options = parts[1:]
		}
	case "/answer":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID = "decision-answer", fields[0]
		note := strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])
		if len(fields) > 1 {
			if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
				request.Choice, note = n, strings.TrimSpace(note[len(fields[1]):])
			}
		}
		request.Text = note
	case "/withdraw-decision":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID = "decision-withdraw", fields[0]
		request.Text = strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])
	case "/decisions":
		request.Action = "decisions"
	case "/status":
		// /status task | state | note posts from this room; /status alone
		// lists the room's latest statuses in the status line.
		if b == nil || b.kind != bufChannel {
			m.setStatus("/status works in a room", true)
			return nil, true
		}
		request.Action, request.Target = "statuses", b.name
		if rest != "" {
			parts := strings.Split(rest, "|")
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			if len(parts) < 2 || !protocol.ValidStatusState(parts[1]) {
				m.setStatus("/status task | working|blocked|done | note", true)
				return nil, true
			}
			request.Action, request.Task, request.State = "status", parts[0], parts[1]
			if len(parts) > 2 {
				request.Text = strings.Join(parts[2:], " | ")
			}
		}
	case "/resolve":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID = "resolve", fields[0]
		request.Text = strings.TrimSpace(rest[len(strings.Fields(rest)[0]):])
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
	switch cmd.kind {
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
		entries, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "room", Target: cmd.target, Seconds: -1, Limit: -1}, translate)
		if err != nil {
			return true, err
		}
		charter := ""
		for _, e := range entries {
			charter = e.Charter
		}
		b.emit(ctx, charterIn{channel: cmd.target, charter: charter})
		return true, nil
	case "react":
		return true, c.React(cmd.target, cmd.text)
	case "chat":
		if (cmd.request.Action == "typing" || cmd.request.Action == "thinking") && !c.Supports("CHAT") {
			return true, nil
		}
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
	case "thread", "search", "query-next", "query-first":
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

// showCharter opens a room's charter as a read-only view, line by line, or
// says in the status line that the room has none. /close returns.
func (m *uiModel) showCharter(c charterIn) {
	if c.charter == "" {
		m.setStatus(c.channel+" has no charter", false)
		return
	}
	name := "charter:" + c.channel
	b := m.find(name)
	if b == nil {
		b = newBuffer(name, bufHelp)
		b.returnTo = m.current
		m.buffers = append(m.buffers, b)
	}
	b.help = []helpLine{{head: "Charter of " + c.channel}}
	for line := range strings.SplitSeq(c.charter, "\n") {
		b.help = append(b.help, helpLine{body: line})
	}
	b.topic = "Charter of " + c.channel + " · /close returns to " + b.returnTo
	b.version++
	m.switchTo(name)
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
