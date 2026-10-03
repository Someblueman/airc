package irc

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
)

type ChatEvent struct {
	Type string `json:"type"`
	ChatEntry
}

func (*ChatEvent) ircEvent() {}

type SignalEvent struct {
	Type string `json:"type"`
	ChatEntry
}

func (*SignalEvent) ircEvent() {}

type EndOfChatEvent struct {
	Type string `json:"type"`
}

func (*EndOfChatEvent) ircEvent() {}

func (c *Client) Chat(request ChatRequest) error {
	if !c.Supports("CHAT") {
		return errors.New("chat additions require CHAT on the daemon")
	}
	text := request.Text
	request.Text = ""
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	line := "CHAT :" + string(data)
	if text != "" {
		line = "@" + protocol.BodyTag + "=" + protocol.EncodeBody(NormalizeMessage(text)) + " " + line
	}
	if len(line) > protocol.MaxLineLength {
		return errors.New("chat request exceeds wire limit")
	}
	return c.Raw(line)
}

// RequestChat waits for the final server confirmation, forwarding unrelated
// events to the caller. Use one outstanding request per connection.
func RequestChat(ctx context.Context, c *Client, request ChatRequest, other func(Event)) ([]ChatEntry, error) {
	if err := c.Chat(request); err != nil {
		return nil, err
	}
	var entries []ChatEntry
	for {
		select {
		case e, ok := <-c.Events():
			if !ok {
				return nil, errors.New("server disconnected before chat confirmation")
			}
			switch v := e.(type) {
			case *ChatEvent:
				if len(entries) >= 1256 {
					return nil, errors.New("server exceeded chat response limit")
				}
				entries = append(entries, v.ChatEntry)
			case *EndOfChatEvent:
				return entries, nil
			case *RawEvent:
				if v.Command != "422" && len(v.Command) == 3 && v.Command[0] >= '4' && v.Command[0] <= '5' {
					return nil, &RejectedError{Code: v.Command, Message: v.Trailing}
				}
				if other != nil {
					other(e)
				}
			default:
				if other != nil {
					other(e)
				}
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
