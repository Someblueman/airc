package main

import (
	"fmt"
	"github.com/Someblueman/airc/pkg/irc"
	"strings"
)

func (m *uiModel) submit() (cmds []uiCmd, quit bool) {
	text := strings.TrimSpace(string(m.input))
	m.input, m.cursor = nil, 0
	if text == "" {
		return nil, false
	}
	b := m.cur()
	if !strings.HasPrefix(text, "/") {
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
	case "/help", "/?":
		m.setStatus("/thread ID · /reply ID text · /react ID emoji · /search text · /pin ID · /pins · /me text · /poll question | option | option · /mute nick · /ban nick · /bans · /close · /quit", false)
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
		if strings.HasPrefix(b.name, "thread:") {
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
