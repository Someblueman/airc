package irc

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type Event interface{ ircEvent() }

type MessageEvent struct {
	protocol.ChatMetadata
	Type      string    `json:"type"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	ID        string    `json:"id"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func (*MessageEvent) ircEvent() {}

// SendReceiptEvent confirms a message the server stored. Queued is set for a
// direct message whose recipient is not connected; it waits in their inbox.
type SendReceiptEvent struct {
	protocol.ChatMetadata
	Type      string    `json:"type"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	ID        string    `json:"id"`
	Seq       uint64    `json:"seq,omitempty"`
	Queued    bool      `json:"queued,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func (*SendReceiptEvent) ircEvent() {}

// MessageEvent returns the receipt in the shape of a delivered message.
func (e *SendReceiptEvent) MessageEvent() *MessageEvent {
	return &MessageEvent{ChatMetadata: e.ChatMetadata, Type: "message", ID: e.ID, ReplyTo: e.ReplyTo, ThreadID: e.ThreadID, Reaction: e.Reaction, From: e.From, Target: e.Target, Message: e.Message, Timestamp: e.Timestamp}
}

type JoinEvent struct {
	Type      string    `json:"type"`
	Agent     string    `json:"agent"`
	Channel   string    `json:"channel"`
	Timestamp time.Time `json:"timestamp"`
}

func (*JoinEvent) ircEvent() {}

type PartEvent struct {
	Type    string `json:"type"`
	Agent   string `json:"agent"`
	Channel string `json:"channel"`
	Reason  string `json:"reason,omitempty"`
}

func (*PartEvent) ircEvent() {}

type PresenceEvent struct {
	Type        string    `json:"type"`
	Nick        string    `json:"nick"`
	Username    string    `json:"username"`
	Channel     string    `json:"channel,omitempty"`
	RealName    string    `json:"real_name"`
	ConnectedAt time.Time `json:"connected_at,omitempty"`
}

func (*PresenceEvent) ircEvent() {}

type EndOfWhoEvent struct {
	Type   string `json:"type"`
	Target string `json:"target"`
}

func (*EndOfWhoEvent) ircEvent() {}

type HistoryEvent struct {
	protocol.ChatMetadata
	Type      string    `json:"type"`
	ReplyTo   string    `json:"reply_to,omitempty"`
	ThreadID  string    `json:"thread_id,omitempty"`
	Reaction  string    `json:"reaction,omitempty"`
	ID        string    `json:"id"`
	Seq       uint64    `json:"seq,omitempty"`
	From      string    `json:"from"`
	Target    string    `json:"target"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

func (*HistoryEvent) ircEvent() {}

// EndOfHistoryEvent closes a history reply. Status is "ok", "more" (the limit was
// reached and later messages remain), or "expired" (the cursor is no longer
// retained, so the latest messages were sent instead). Servers that predate
// cursors leave it empty.
type EndOfHistoryEvent struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Status string `json:"status,omitempty"`
	// Cursor is the server watermark when caught up, or the last returned ID
	// when more remain. Older servers leave it empty.
	Cursor string `json:"cursor,omitempty"`
}

func (*EndOfHistoryEvent) ircEvent() {}

type AgentInfo struct {
	Nick        string    `json:"nick"`
	Channels    []string  `json:"channels"`
	ConnectedAt time.Time `json:"connected_at"`
}

type AgentsEvent struct {
	Type  string    `json:"type"`
	Agent AgentInfo `json:"agent"`
}

func (*AgentsEvent) ircEvent() {}

type EndOfAgentsEvent struct {
	Type string `json:"type"`
}

func (*EndOfAgentsEvent) ircEvent() {}

// TopicEvent reports a channel's header: in reply to a query or on joining
// (SetBy empty), or live when someone changes it. An empty Topic means none.
type TopicEvent struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	Topic   string `json:"topic"`
	SetBy   string `json:"set_by,omitempty"`
}

func (*TopicEvent) ircEvent() {}

// ChannelInfo describes a channel the server knows about, whether or not anyone
// is connected to it.
type ChannelInfo struct {
	Name         string    `json:"name"`
	Members      int       `json:"members"`
	Messages     int       `json:"messages"`
	LastActivity time.Time `json:"last_activity,omitzero"`
	Topic        string    `json:"topic,omitempty"`
}

// ChannelEvent is one entry of the reply to Client.Channels.
type ChannelEvent struct {
	Type    string      `json:"type"`
	Channel ChannelInfo `json:"channel"`
}

func (*ChannelEvent) ircEvent() {}

type EndOfChannelsEvent struct {
	Type string `json:"type"`
}

func (*EndOfChannelsEvent) ircEvent() {}

type NickEvent struct {
	Type string `json:"type"`
	Old  string `json:"old"`
	Nick string `json:"nick"`
}

func (*NickEvent) ircEvent() {}

type QuitEvent struct {
	Type   string `json:"type"`
	Agent  string `json:"agent"`
	Reason string `json:"reason,omitempty"`
}

func (*QuitEvent) ircEvent() {}

type ConnectionEvent struct {
	Type      string `json:"type"`
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

func (*ConnectionEvent) ircEvent() {}

type RawEvent struct {
	Type     string            `json:"type"`
	Command  string            `json:"command"`
	Prefix   string            `json:"prefix,omitempty"`
	Params   []string          `json:"params,omitempty"`
	Trailing string            `json:"trailing,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

func (*RawEvent) ircEvent() {}

func decodeEvent(line string) (Event, *protocol.Command, error) {
	command, err := protocol.Parse(line)
	if err != nil {
		return nil, nil, err
	}
	return eventFromCommand(command), &command, nil
}

func eventFromCommand(command protocol.Command) Event {
	parts := strings.SplitN(command.Prefix, "!", 2)
	agent := parts[0]
	now := time.Now().UTC()
	switch command.Name {
	case "PRIVMSG":
		target, _ := command.Param(0)
		timestamp := now
		if raw := command.Tags["time"]; raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				timestamp = parsed
			}
		}
		body := command.Trailing
		if encoded, ok := command.Tags[protocol.BodyTag]; ok {
			if full, err := protocol.DecodeBody(encoded); err == nil {
				body = full
			}
		}
		return &MessageEvent{ChatMetadata: protocol.DecodeChat(command.Tags[protocol.ChatTag]), Type: "message", ID: command.Tags["msgid"], ReplyTo: command.Tags[protocol.ReplyTag], ThreadID: command.Tags[protocol.ThreadTag], Reaction: command.Tags[protocol.ReactionTag], From: agent, Target: target, Message: body, Timestamp: timestamp}
	case "JOIN":
		channel, _ := command.Param(0)
		return &JoinEvent{Type: "join", Agent: agent, Channel: channel, Timestamp: now}
	case "PART":
		channel, _ := command.Param(0)
		return &PartEvent{Type: "part", Agent: agent, Channel: channel, Reason: command.Trailing}
	case "QUIT":
		return &QuitEvent{Type: "quit", Agent: agent, Reason: command.Trailing}
	case "NICK":
		nick := command.Trailing
		if nick == "" {
			nick, _ = command.Param(0)
		}
		return &NickEvent{Type: "nick", Old: agent, Nick: nick}
	case "352":
		if len(command.Params) >= 7 {
			channel, username, nick := command.Params[1], command.Params[2], command.Params[5]
			return &PresenceEvent{Type: "agent", Nick: nick, Username: username, Channel: channel, RealName: command.Trailing}
		}
	case "315":
		target, _ := command.Param(1)
		return &EndOfWhoEvent{Type: "end_of_who", Target: target}
	case "773":
		var card protocol.AgentCard
		if json.Unmarshal([]byte(command.Trailing), &card) == nil {
			return &DirectoryEvent{Type: "agent", AgentCard: card}
		}
	case "774":
		return &EndOfDirectoryEvent{Type: "end_of_directory"}
	case "775":
		var result protocol.AdminResult
		if json.Unmarshal([]byte(command.Trailing), &result) == nil {
			return &AdminEvent{Type: "admin", AdminResult: result}
		}
	case "776":
		return &EndOfAdminEvent{Type: "end_of_admin"}
	case "777", "780":
		var entry protocol.ChatEntry
		if json.Unmarshal([]byte(command.Trailing), &entry) == nil {
			if command.Name == "780" {
				return &SignalEvent{Type: "signal", ChatEntry: entry}
			}
			return &ChatEvent{Type: "chat", ChatEntry: entry}
		}
	case "778":
		return &EndOfChatEvent{Type: "end_of_chat"}
	case "760":
		if message, err := protocol.DecodeMessageMetadata(command.Trailing); err == nil {
			return &HistoryEvent{ChatMetadata: message.ChatMetadata, Type: "history", ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	case "761":
		target, _ := command.Param(1)
		status, _ := command.Param(2)
		cursor, _ := command.Param(3)
		return &EndOfHistoryEvent{Type: "end_of_history", Target: target, Status: status, Cursor: cursor}
	case "763":
		var agent AgentInfo
		if json.Unmarshal([]byte(command.Trailing), &agent) == nil {
			return &AgentsEvent{Type: "agents", Agent: agent}
		}
	case "764":
		return &EndOfAgentsEvent{Type: "end_of_agents"}
	case "TOPIC":
		channel, _ := command.Param(0)
		return &TopicEvent{Type: "topic", Channel: channel, Topic: command.Trailing, SetBy: agent}
	case "332":
		channel, _ := command.Param(1)
		return &TopicEvent{Type: "topic", Channel: channel, Topic: command.Trailing}
	case "331":
		channel, _ := command.Param(1)
		return &TopicEvent{Type: "topic", Channel: channel}
	case "768":
		var info ChannelInfo
		if json.Unmarshal([]byte(command.Trailing), &info) == nil {
			return &ChannelEvent{Type: "channel", Channel: info}
		}
	case "769":
		return &EndOfChannelsEvent{Type: "end_of_channels"}
	case "762":
		if message, err := protocol.DecodeMessageMetadata(command.Trailing); err == nil {
			queued, _ := command.Param(2)
			return &SendReceiptEvent{ChatMetadata: message.ChatMetadata, Type: "send_receipt", ID: message.ID, ReplyTo: message.ReplyTo, ThreadID: message.ThreadID, Reaction: message.Reaction, Seq: message.Seq, Queued: queued == "queued", From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	}
	return &RawEvent{Type: "raw", Command: command.Name, Prefix: command.Prefix, Params: command.Params, Trailing: command.Trailing, Tags: command.Tags}
}

// Mentions returns the lower-cased nicknames a message text tags with @nick or
// addresses with a leading "nick:".
func Mentions(text string) []string { return protocol.Mentions(text) }

// Mentions reports whether the message tags or addresses nick.
func (e *MessageEvent) Mentions(nick string) bool { return protocol.MentionsNick(e.Message, nick) }

// Mentions reports whether the message tags or addresses nick.
func (e *HistoryEvent) Mentions(nick string) bool { return protocol.MentionsNick(e.Message, nick) }
