package irc

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type Event interface{ ircEvent() }

type MessageEvent struct {
	Type      string    `json:"type"`
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
	Type      string    `json:"type"`
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
	return &MessageEvent{Type: "message", ID: e.ID, From: e.From, Target: e.Target, Message: e.Message, Timestamp: e.Timestamp}
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
	Type      string    `json:"type"`
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
		return &MessageEvent{Type: "message", ID: command.Tags["msgid"], From: agent, Target: target, Message: command.Trailing, Timestamp: timestamp}
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
	case "760":
		if message, err := protocol.DecodeMessageMetadata(command.Trailing); err == nil {
			return &HistoryEvent{Type: "history", ID: message.ID, Seq: message.Seq, From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	case "761":
		target, _ := command.Param(1)
		status, _ := command.Param(2)
		return &EndOfHistoryEvent{Type: "end_of_history", Target: target, Status: status}
	case "763":
		var agent AgentInfo
		if json.Unmarshal([]byte(command.Trailing), &agent) == nil {
			return &AgentsEvent{Type: "agents", Agent: agent}
		}
	case "764":
		return &EndOfAgentsEvent{Type: "end_of_agents"}
	case "762":
		if message, err := protocol.DecodeMessageMetadata(command.Trailing); err == nil {
			queued, _ := command.Param(2)
			return &SendReceiptEvent{Type: "send_receipt", ID: message.ID, Seq: message.Seq, Queued: queued == "queued", From: message.From, Target: message.Target, Message: message.Message, Timestamp: message.Timestamp}
		}
	}
	return &RawEvent{Type: "raw", Command: command.Name, Prefix: command.Prefix, Params: command.Params, Trailing: command.Trailing, Tags: command.Tags}
}
