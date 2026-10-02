package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/pkg/irc"
)

type queryIn struct {
	name     string
	thread   bool
	messages []*irc.HistoryEvent
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
		for i := len(b.items) - 1; i >= 0; i-- {
			if event, ok := b.items[i].(*irc.MessageEvent); ok && event.ID != "" {
				fields[0] = event.ID
				break
			}
		}
	}
	request := irc.ChatRequest{}
	switch name {
	case "/thread", "/follow", "/unfollow":
		if !need(1) {
			return nil, true
		}
		return []uiCmd{{kind: name[1:], target: fields[0]}}, true
	case "/reply", "/react":
		if !need(2) {
			return nil, true
		}
		return []uiCmd{{kind: name[1:], target: fields[0], text: strings.Join(fields[1:], " ")}}, true
	case "/search":
		if !need(1) || b == nil {
			return nil, true
		}
		return []uiCmd{{kind: "search", target: b.name, text: rest}}, true
	case "/pin", "/unpin", "/correct", "/retract", "/prepare", "/waiting", "/cancel", "/vote", "/results", "/close-poll":
		if !need(1) {
			return nil, true
		}
		request.Action, request.ID = name[1:], fields[0]
		if len(fields) > 1 {
			request.Text = strings.Join(fields[1:], " ")
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
	switch cmd.kind {
	case "close-query":
		delete(b.threads, strings.TrimPrefix(cmd.target, "thread:"))
		delete(b.observed, cmd.target)
		return true, c.Raw("UNOBSERVE " + cmd.target)
	case "reply":
		return true, c.Reply(cmd.target, cmd.text)
	case "react":
		return true, c.React(cmd.target, cmd.text)
	case "chat":
		if (cmd.request.Action == "typing" || cmd.request.Action == "thinking") && !c.Supports("CHAT") {
			return true, nil
		}
		entries, err := irc.RequestChat(ctx, c, *cmd.request, translate)
		if err == nil {
			for _, e := range entries {
				if e.Action == "typing" || e.Action == "thinking" {
					continue
				}
				b.emit(ctx, statusIn{text: chatEntryText(e)})
			}
		}
		return true, err
	case "thread":
		if b.threads == nil {
			b.threads = map[string]bool{}
		}
		if len(b.threads) >= 16 && !b.threads[cmd.target] {
			return true, errors.New("UI thread limit reached (16)")
		}
		page, err := fetchHistory(ctx, c, "thread:"+cmd.target, "*", 100, translate)
		if err != nil {
			return true, err
		}
		if len(page.messages) > 0 {
			first := page.messages[0]
			cmd.target = first.ID
			if first.ThreadID != "" {
				cmd.target = first.ThreadID
			}
		}
		b.threads[cmd.target] = true
		b.emit(ctx, queryIn{name: "thread:" + cmd.target, thread: true, messages: page.messages})
		return true, b.subscribe(ctx, c, []string{"thread:" + cmd.target}, translate)
	case "search":
		if err := c.Search(cmd.target, cmd.text, "", "", 50); err != nil {
			return true, err
		}
		page, err := awaitHistory(ctx, c, cmd.target, translate)
		if err != nil {
			return true, err
		}
		b.emit(ctx, queryIn{name: "search:" + cmd.target, messages: page.messages})
		return true, nil
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
		err := runFollow(cmd.kind, args, &output, &output)
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
		for {
			select {
			case event, ok := <-c.Events():
				if !ok {
					return true, errors.New("disconnected during moderation")
				}
				if err := serverError(event); err != nil {
					return true, err
				}
				switch e := event.(type) {
				case *irc.AdminEvent:
					b.emit(ctx, statusIn{text: adminText(e.AdminResult)})
				case *irc.EndOfAdminEvent:
					return true, nil
				default:
					translate(event)
				}
			case <-ctx.Done():
				return true, ctx.Err()
			}
		}
	}
	return false, nil
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
	if !q.thread {
		b.items, b.seen = nil, map[string]struct{}{}
		b.version++
	}
	for _, e := range q.messages {
		b.add(&irc.MessageEvent{ChatMetadata: e.ChatMetadata, Type: "message", ID: e.ID, From: e.From, Target: e.Target, Message: e.Message, ReplyTo: e.ReplyTo, ThreadID: e.ThreadID, Reaction: e.Reaction, Timestamp: e.Timestamp})
	}
	m.switchTo(q.name)
}

func (m *uiModel) typingCommand(cmds []uiCmd) []uiCmd {
	b := m.cur()
	if b != nil && b.kind == bufChannel && m.now().Sub(m.lastTyping) >= 3*time.Second {
		m.lastTyping = m.now()
		cmds = append(cmds, uiCmd{kind: "chat", request: &irc.ChatRequest{Action: "typing", Target: b.name, Seconds: 10}})
	}
	return cmds
}
