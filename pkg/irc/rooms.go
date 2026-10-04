package irc

import (
	"errors"
	"github.com/Someblueman/airc/internal/protocol"
	"strings"
	"unicode/utf8"
)

type KickEvent struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	Agent   string `json:"agent"`
	By      string `json:"by"`
	Reason  string `json:"reason,omitempty"`
}

func (*KickEvent) ircEvent() {}

type MonitorEvent struct {
	Type   string   `json:"type"`
	Online bool     `json:"online"`
	Nicks  []string `json:"nicks"`
}

func (*MonitorEvent) ircEvent() {}

func (c *Client) Kick(channel, nick, reason string) error {
	return c.command("KICK", []string{channel, nick}, reason)
}
func (c *Client) SetOperator(channel, nick string, enabled bool) error {
	mode := "-o"
	if enabled {
		mode = "+o"
	}
	return c.command("MODE", []string{channel, mode, nick}, "")
}
func (c *Client) Operators(channel string) error {
	return c.command("MODE", []string{channel}, "")
}

// Away marks this persistent session away. Empty text clears it. The state is
// restored after reconnect; for one-shot agents use Presence with a TTL.
func (c *Client) Away(reason string) error {
	if c.Ephemeral() {
		return errors.New("AWAY requires a persistent session; use Presence for one-shot agents")
	}
	line, err := commandLine("AWAY", nil, reason)
	if err != nil || len(reason) > 240 {
		return errors.New("away reason must be a single line up to 240 bytes")
	}
	c.mu.Lock()
	c.away = reason
	c.mu.Unlock()
	return c.writeLine(line)
}

// Monitor replaces the desired online/offline subscription, bounded to 128
// nicknames. It reports initial states and restores the list after reconnect.
func (c *Client) Monitor(nicks ...string) error {
	if len(nicks) > 128 {
		return errors.New("monitor supports at most 128 nicknames")
	}
	for _, nick := range nicks {
		if !validMonitorNick(nick) {
			return errors.New("invalid monitor nickname")
		}
	}
	c.mu.Lock()
	c.monitoring = append([]string(nil), nicks...)
	c.mu.Unlock()
	return c.restoreMonitor(nicks)
}
func validMonitorNick(nick string) bool {
	if nick == "" || len(nick) > 30 {
		return false
	}
	for i, r := range nick {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("[]\\`_^{}|", r) || i > 0 && (r == '-' || r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}
func (c *Client) restoreMonitor(nicks []string) error {
	if err := c.writeLine("MONITOR C\r\n"); err != nil {
		return err
	}
	if len(nicks) == 0 {
		return nil
	}
	return c.command("MONITOR", []string{"+", strings.Join(nicks, ",")}, "")
}

// BotReply marks automated output while retaining reply and thread context.
// The marker is descriptive, never an authorization claim.
func (c *Client) BotReply(parent, text string) error {
	if !c.Supports("BOT_REPLIES") || !protocol.ValidMessageID(parent) {
		return errors.New("bot replies require BOT_REPLIES and a valid parent")
	}
	text = NormalizeMessage(text)
	if len(text) > 4096 || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return errors.New("invalid bot response")
	}
	return c.Raw("@+airc/bot=1;" + protocol.BodyTag + "=" + protocol.EncodeBody(text) + " REPLY " + parent + " :" + protocol.Preview(text))
}
