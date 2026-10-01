package irc

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

func (c *Client) Join(channel string) error {
	line, err := commandLine("JOIN", []string{channel}, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	if _, exists := c.joined[channel]; !exists && len(c.joined) >= 64 {
		c.mu.Unlock()
		return errors.New("client is already tracking 64 channels")
	}
	c.joined[channel] = struct{}{}
	c.mu.Unlock()
	return c.writeLine(line)
}

func (c *Client) SetNick(nick string) error {
	line, err := commandLine("NICK", []string{nick}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) Part(channel, reason string) error {
	line, err := commandLine("PART", []string{channel}, reason)
	if err != nil {
		return err
	}
	if err := c.writeLine(line); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.joined, channel)
	c.mu.Unlock()
	return nil
}

// Send publishes a message. Text may span several lines (the server must
// support it; see Multiline). Line endings are normalized to LF.
func (c *Client) Send(target, message string) error { return c.sendText("PRIVMSG", target, message) }

func (c *Client) Notice(target, message string) error { return c.sendText("NOTICE", target, message) }

// NormalizeMessage returns message as Send will transmit it, with CRLF and CR
// converted to LF. Use it to compare a message you sent with what comes back.
func NormalizeMessage(message string) string { return protocol.NormalizeNewlines(message) }

func (c *Client) sendText(command, target, message string) error {
	message = NormalizeMessage(message)
	if strings.ContainsAny(target, "\r\n\x00") || strings.ContainsRune(message, 0) {
		return errors.New("IRC values may not contain NUL, and targets may not contain line breaks")
	}
	if len(message) > 4096 {
		return errors.New("message exceeds 4096 bytes")
	}
	if !utf8.ValidString(message) {
		return errors.New("message must be valid UTF-8")
	}
	if !strings.Contains(message, "\n") {
		line, err := commandLine(command, []string{target}, message)
		if err != nil {
			return err
		}
		return c.writeLine(line)
	}
	if !c.Multiline() {
		return errors.New("this server does not support multi-line messages; send a single line or upgrade aircd")
	}
	line, err := commandLine(command, []string{target}, protocol.Preview(message))
	if err != nil {
		return err
	}
	return c.writeLine("@" + protocol.BodyTag + "=" + protocol.EncodeBody(message) + " " + line)
}

func (c *Client) Who(target string) error {
	params := []string{}
	if target != "" {
		params = append(params, target)
	}
	line, err := commandLine("WHO", params, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) WhoIs(nick string) error {
	line, err := commandLine("WHOIS", []string{nick}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) Names(channel string) error {
	line, err := commandLine("NAMES", []string{channel}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) History(channel string, limit int) error {
	params := []string{channel}
	if limit > 0 {
		params = append(params, fmt.Sprint(limit))
	}
	line, err := commandLine("HISTORY", params, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// HistoryAfter requests the messages for a channel or nickname that follow the
// message with ID after, oldest first. An empty after behaves like History. The
// reply ends with an EndOfHistoryEvent whose Status says whether more remain or
// the cursor has expired. target may be a nickname to read direct messages
// addressed to it. Older servers ignore the cursor, so check the status.
func (c *Client) HistoryAfter(target, after string, limit int) error {
	if after == "" {
		return c.History(target, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	line, err := commandLine("HISTORY", []string{target, fmt.Sprint(limit), after}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// Observe subscribes to live messages without joining. Targets are channel
// names, or "@nick" for the direct messages addressed to a nickname. The server
// acknowledges each target with numeric 765.
func (c *Client) Observe(targets ...string) error {
	line, err := commandLine("OBSERVE", []string{strings.Join(targets, ",")}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// Topic asks for a channel's header; the reply is a TopicEvent.
func (c *Client) Topic(channel string) error {
	line, err := commandLine("TOPIC", []string{channel}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// SetTopic sets a channel's header, or clears it when text is empty. Every
// observer and member of the channel receives a TopicEvent.
func (c *Client) SetTopic(channel, text string) error {
	if strings.ContainsAny(channel, " \t\r\n\x00") || strings.ContainsAny(text, "\r\n\x00") || channel == "" {
		return errors.New("IRC values may not contain line breaks, NUL, or (for a channel) spaces")
	}
	// An explicit colon marks "set", even when the text is empty.
	return c.writeLine("TOPIC " + channel + " :" + text + "\r\n")
}

// Channels asks for every channel the server knows about, including ones with
// only retained history. The reply is ChannelEvents then an EndOfChannelsEvent.
func (c *Client) Channels() error { return c.writeLine("CHANNELS\r\n") }

// Raw sends one parsed IRC-style command, which is useful for less common extensions.
func (c *Client) Raw(line string) error {
	if strings.ContainsAny(line, "\r\n\x00") {
		return errors.New("raw command must contain exactly one line")
	}
	if _, err := protocol.Parse(line); err != nil {
		return err
	}
	return c.writeLine(strings.TrimSpace(line) + "\r\n")
}

func commandLine(name string, params []string, trailing string) (string, error) {
	for _, param := range params {
		if param == "" || strings.HasPrefix(param, ":") || strings.ContainsAny(param, " \t\r\n\x00") {
			return "", errors.New("IRC parameters must be non-empty single-line tokens")
		}
	}
	if strings.ContainsAny(trailing, "\r\n\x00") {
		return "", errors.New("IRC trailing text may not contain line breaks or NUL")
	}
	line := protocol.Format("", name, params, trailing)
	if !strings.HasSuffix(line, "\r\n") {
		return "", errors.New("IRC command exceeds maximum line length")
	}
	return line, nil
}
