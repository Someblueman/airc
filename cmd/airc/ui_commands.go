package main

import (
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
)

func (m *uiModel) submit() (cmds []uiCmd, quit bool) {
	if m.uncertainID != "" {
		if !m.connected {
			m.setStatus("Offline; draft and recovery handle kept", true)
			return nil, false
		}
		cmd := uiCmd{kind: "retry-send", target: m.uncertainID}
		m.pending = &cmd
		return []uiCmd{cmd}, false
	}
	originalView := m.current
	text := strings.TrimSpace(string(m.input))
	m.statusError = false
	defer func() {
		if len(cmds) == 1 && confirmedUICmd(cmds[0]) {
			if !m.connected {
				cmds = nil
				m.setStatus("Offline; draft kept", true)
				return
			}
			m.pending = &cmds[0]
			m.setStatus("Waiting for confirmation; draft kept", false)
			return
		}
		if quit || !m.statusError {
			if m.current == originalView {
				m.input, m.cursor = nil, 0
			} else {
				delete(m.drafts, originalView)
			}
		}
	}()
	if text == "" {
		return nil, false
	}
	b := m.cur()
	if !strings.HasPrefix(text, "/") {
		if m.replyTo != "" {
			return []uiCmd{{kind: "reply", target: m.replyTo, text: text}}, false
		}
		if b != nil && b.kind == bufQuery && strings.HasPrefix(b.name, "thread:") {
			id := strings.TrimPrefix(b.name, "thread:")
			if len(b.items) > 0 {
				if last, ok := b.items[len(b.items)-1].(*irc.MessageEvent); ok {
					id = last.ID
				}
			}
			return []uiCmd{{kind: "reply", target: id, text: text}}, false
		}
		if b == nil || b.kind != bufChannel {
			m.setStatus("This view is read-only: use /msg nick text, or Tab to a channel", true)
			return nil, false
		}
		return []uiCmd{{kind: "send", target: b.name, text: text}}, false
	}
	name, rest, _ := strings.Cut(text, " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(name) {
	case "/quit", "/q", "/exit":
		return nil, true
	case "/next", "/first":
		if b == nil || b.query == nil {
			m.setStatus("Open a thread or search first", true)
			return nil, false
		}
		kind := "query-next"
		if strings.EqualFold(name, "/first") {
			kind = "query-first"
		}
		return []uiCmd{{kind: kind, target: b.name}}, false
	case "/help", "/?":
		m.setStatus("Ctrl-Up/Down: select · Ctrl-O: context · Ctrl-R: reply · /context ID · /thread ID · /reply ID text · /react ID emoji · /search text · /next · /first · /pin ID · /pins · /me text · /poll question | option | option · /mute nick · /ban nick · /bans · /op nick · /deop nick · /kick nick · /disconnect nick · /away [reason] · /close · /quit", false)
	case "/topic":
		if b == nil || b.kind != bufChannel {
			m.setStatus("/topic works in a channel", true)
		} else if rest == "" {
			if b.topic == "" {
				m.setStatus(b.name+" has no topic. Set one with /topic text", false)
			} else {
				m.setStatus("Topic: "+b.topic, false)
			}
		} else {
			if rest == "-" {
				rest = ""
			}
			return []uiCmd{{kind: "topic", target: b.name, text: rest}}, false
		}
	case "/msg", "/m":
		nick, body, _ := strings.Cut(rest, " ")
		if nick == "" || strings.TrimSpace(body) == "" {
			m.setStatus("usage: /msg nick text", true)
			break
		}
		return []uiCmd{{kind: "send", target: nick, text: strings.TrimSpace(body)}}, false
	case "/join", "/j":
		channel := channelName(rest)
		if !isChannel(channel) {
			m.setStatus("usage: /join #channel", true)
			break
		}
		m.ensureChannel(channel)
		return append(m.switchTo(channel), uiCmd{kind: "observe", target: channel}), false
	case "/close":
		if b == nil || b.kind != bufChannel && b.kind != bufQuery {
			m.setStatus("Only channels can be closed", true)
			break
		}
		for i, candidate := range m.buffers {
			if candidate == b {
				m.buffers = append(m.buffers[:i], m.buffers[i+1:]...)
				break
			}
		}
		next := m.firstName()
		if b.kind == bufQuery && m.find(b.returnTo) != nil {
			next = b.returnTo
		}
		cmds := m.switchTo(next)
		if b.kind == bufQuery {
			cmds = append(cmds, uiCmd{kind: "close-query", target: b.name})
		}
		return cmds, false
	default:
		if cmds, handled := m.submitChat(strings.ToLower(name), rest, b); handled {
			return cmds, false
		}
		m.setStatus(fmt.Sprintf("Unknown command %s (try /help)", name), true)
	}
	return nil, false
}
