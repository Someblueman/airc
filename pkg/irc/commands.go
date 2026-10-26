package irc

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

// AllDirectMessages selects every retained/live direct message for human
// oversight. Check Supports("DM_AUDIT") before using it with older daemons.
const AllDirectMessages = protocol.AllDirectMessages

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
	return c.command("NICK", []string{nick}, "")
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

// Reply sends to the parent's room, or to the other participant in a DM.
// The parent must still be retained by a server advertising REPLIES.
func (c *Client) Reply(parent, message string) error {
	if !protocol.ValidMessageID(parent) {
		return errors.New("reply requires a 32-character message ID")
	}
	if !c.Supports("REPLIES") {
		return errors.New("replies need a daemon with REPLIES; upgrade/restart when active work is finished")
	}
	return c.sendText("REPLY", parent, message)
}

func (c *Client) Notice(target, message string) error { return c.sendText("NOTICE", target, message) }

// NormalizeMessage returns message as Send will transmit it, with CRLF and CR
// converted to LF. Use it to compare a message you sent with what comes back.
func NormalizeMessage(message string) string { return protocol.NormalizeNewlines(message) }

func (c *Client) sendText(command, target, message string) error {
	return c.sendTextWithID(command, target, message, "")
}

func (c *Client) SendWithID(target, message, requestID string) error {
	return c.sendTextWithID("PRIVMSG", target, message, requestID)
}

// RetryRequest retrieves a previous receipt without ever creating a message.
// Numerics 762 and 488 distinguish a retained receipt from an unknown outcome.
func (c *Client) RetryRequest(requestID string) error {
	if !c.Supports("SAFE_RETRY") || !protocol.ValidRequestID(requestID) {
		return errors.New("receipt recovery requires SAFE_RETRY and a valid request ID")
	}
	return c.Raw("RETRY " + requestID)
}
func (c *Client) ReplyWithID(parent, message, requestID string) error {
	return c.ReplyWith(parent, message, SendOptions{RequestID: requestID})
}

// SendOptions are the tags a send or reply may carry: a RequestID for safe
// retries (IDEMPOTENCY) and a Run label (RUN), 1-64 letters, digits, '.',
// '_' or '-', kept in the message's metadata and searchable. A daemon
// without RUN ignores the label.
type SendOptions struct {
	RequestID string
	Run       string
	// Paste is the ID of a paste the message shares (PASTE); its body
	// should end with the paste's reference line (protocol.PasteMessageBody).
	Paste string
}

// SendWith publishes a message with options.
func (c *Client) SendWith(target, message string, options SendOptions) error {
	return c.sendTagged("PRIVMSG", target, message, options)
}

// ReplyWith is Reply with options.
func (c *Client) ReplyWith(parent, message string, options SendOptions) error {
	if !c.Supports("REPLIES") || !protocol.ValidMessageID(parent) {
		return errors.New("reply needs REPLIES and a valid message ID")
	}
	return c.sendTagged("REPLY", parent, message, options)
}

func (c *Client) sendTextWithID(command, target, message, requestID string) error {
	return c.sendTagged(command, target, message, SendOptions{RequestID: requestID})
}

// sendTagged sends a message with its optional request ID and run label
// and, for one that shares a paste, the paste's ID in the PasteTag.
func (c *Client) sendTagged(command, target, message string, options SendOptions) error {
	pasteID := options.Paste
	tags := ""
	if options.RequestID != "" {
		if !c.Supports("IDEMPOTENCY") || !protocol.ValidRequestID(options.RequestID) {
			return errors.New("safe retries require IDEMPOTENCY and a request ID of 1-64 letters/digits/-/_")
		}
		tags = "@" + protocol.RequestIDTag + "=" + options.RequestID + " "
	}
	if options.Run != "" {
		if !protocol.ValidRunID(options.Run) {
			return errors.New("a run label is 1-64 letters, digits, '.', '_' or '-'")
		}
		if tags == "" {
			tags = "@"
		} else {
			tags = strings.TrimSuffix(tags, " ") + ";"
		}
		tags += protocol.RunTag + "=" + options.Run + " "
	}
	if pasteID != "" {
		if !c.Supports("PASTE") || !protocol.ValidMessageID(pasteID) {
			return errors.New("sharing a paste requires PASTE and the paste's ID")
		}
		if tags == "" {
			tags = "@"
		} else {
			tags = strings.TrimSuffix(tags, " ") + ";"
		}
		tags += protocol.PasteTag + "=" + pasteID + " "
	}
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
		return c.writeLine(tags + line)
	}
	if !c.Multiline() {
		return errors.New("this server does not support multi-line messages; send a single line or upgrade aircd")
	}
	line, err := commandLine(command, []string{target}, protocol.Preview(message))
	if err != nil {
		return err
	}
	if tags == "" {
		tags = "@"
	} else {
		tags = strings.TrimSuffix(tags, " ") + ";"
	}
	return c.writeLine(tags + protocol.BodyTag + "=" + protocol.EncodeBody(message) + " " + line)
}

func (c *Client) Who(target string) error {
	params := []string{}
	if target != "" {
		params = append(params, target)
	}
	return c.command("WHO", params, "")
}

func (c *Client) WhoIs(nick string) error {
	return c.command("WHOIS", []string{nick}, "")
}

func (c *Client) Names(channel string) error {
	return c.command("NAMES", []string{channel}, "")
}

func (c *Client) History(channel string, limit int) error {
	params := []string{channel}
	if limit > 0 {
		params = append(params, fmt.Sprint(limit))
	}
	return c.command("HISTORY", params, "")
}

// HistoryAfter requests the messages for a channel or nickname that follow the
// message with ID after, oldest first. An empty after behaves like History. The
// reply ends with an EndOfHistoryEvent whose Status says whether more remain or
// the cursor has expired. target may be a nickname to read direct messages
// addressed to it. Older servers ignore the cursor, so check the status.
func (c *Client) HistoryAfter(target, after string, limit int) error {
	return c.HistoryWith(target, after, limit, HistoryOptions{})
}

// HistoryOptions narrow a history read on a daemon with RUN: Run keeps only
// the messages posted under that run label.
type HistoryOptions struct {
	Run string
}

// HistoryWith is HistoryAfter with options. Options other than the zero
// value need a daemon with RUN.
func (c *Client) HistoryWith(target, after string, limit int, options HistoryOptions) error {
	if options.Run == "" {
		if after == "" {
			return c.History(target, limit)
		}
		if limit <= 0 {
			limit = 50
		}
		return c.command("HISTORY", []string{target, fmt.Sprint(limit), after}, "")
	}
	if !c.Supports("RUN") {
		return errors.New("run filters need a daemon with RUN; upgrade/restart when active work is finished")
	}
	if !protocol.ValidRunID(options.Run) {
		return errors.New("a run label is 1-64 letters, digits, '.', '_' or '-'")
	}
	if limit <= 0 {
		limit = 50
	}
	if after == "" {
		after = "-"
	}
	return c.command("HISTORY", []string{target, fmt.Sprint(limit), after, "run=" + options.Run}, "")
}

// Observe subscribes to live messages without joining. Targets are channel
// names, "@nick" for an inbox, or AllDirectMessages for human oversight. The server
// acknowledges each target with numeric 765. Observe also selects typed 780
// signal delivery for all observations on this client.
func (c *Client) Observe(targets ...string) error {
	line, err := commandLine("OBSERVE", []string{strings.Join(targets, ",")}, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.signalsRaw = false
	c.mu.Unlock()
	return c.writeLine(line)
}

// ObserveSignalsRaw selects raw numeric 780 delivery before subscribing.
// Selection applies to all observations on this client until another Observe
// or ObserveSignalsRaw call; numeric 777 remains typed. Serialize mode changes
// and consume the prior observation acknowledgement before switching.
func (c *Client) ObserveSignalsRaw(selector string) error {
	line, err := commandLine("OBSERVE", []string{selector}, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.signalsRaw = true
	c.mu.Unlock()
	return c.writeLine(line)
}

// Topic asks for a channel's header; the reply is a TopicEvent.
func (c *Client) Topic(channel string) error {
	return c.command("TOPIC", []string{channel}, "")
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

// ChannelsRaw asks for every known channel, preserving the raw 768/769 envelopes.
// Raw decoding remains selected until another Channels or ChannelsRaw call.
// Callers must exclusively consume Events until the matching 769 arrives and
// must not overlap CHANNELS requests on the same client.
func (c *Client) ChannelsRaw() error {
	c.mu.Lock()
	c.channelsRaw = true
	c.mu.Unlock()
	return c.writeLine("CHANNELS\r\n")
}

// Channels asks for every known channel, including rooms with only retained
// history or a topic. It selects ChannelEvents followed by EndOfChannelsEvent.
// Callers must finish consuming the previous CHANNELS reply first.
func (c *Client) Channels() error {
	c.mu.Lock()
	c.channelsRaw = false
	c.mu.Unlock()
	return c.writeLine("CHANNELS\r\n")
}

// AgentsRaw asks for the connected-agent snapshot while preserving the raw
// 763/764 envelopes. Raw mode remains selected until Agents or AgentsRaw is
// called again. Serialize mode changes and consume the prior terminator first.
func (c *Client) AgentsRaw() error {
	c.mu.Lock()
	c.agentsRaw = true
	c.mu.Unlock()
	return c.writeLine("AGENTS\r\n")
}

// Agents asks for the connected-agent snapshot and restores typed 763/764 events.
func (c *Client) Agents() error {
	c.mu.Lock()
	c.agentsRaw = false
	c.mu.Unlock()
	return c.writeLine("AGENTS\r\n")
}

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

// command validates and sends one untagged command line.
func (c *Client) command(name string, params []string, trailing string) error {
	line, err := commandLine(name, params, trailing)
	if err != nil {
		return err
	}
	return c.writeLine(line)
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
