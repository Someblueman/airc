package irc

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// maxResponseEntries bounds how many entries RequestChat and RequestCheck
// accept before treating the server as misbehaving.
const maxResponseEntries = 1256

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
	text, run := request.Text, request.Run
	request.Text = ""
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	line := "CHAT :" + string(data)
	// The body and the run label travel as tags, which a daemon that does
	// not know one ignores.
	var tags []string
	if text != "" {
		tags = append(tags, protocol.BodyTag+"="+protocol.EncodeBody(NormalizeMessage(text)))
	}
	if run != "" {
		if !protocol.ValidRunID(run) {
			return errors.New("a run label is 1-64 letters, digits, '.', '_' or '-'")
		}
		tags = append(tags, protocol.RunTag+"="+run)
	}
	if len(tags) > 0 {
		line = "@" + strings.Join(tags, ";") + " " + line
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
				if len(entries) >= maxResponseEntries {
					return nil, errors.New("server exceeded chat response limit")
				}
				entries = append(entries, v.ChatEntry)
			case *EndOfChatEvent:
				return entries, nil
			case *RawEvent:
				if err := v.Rejection(); err != nil {
					return nil, err
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
