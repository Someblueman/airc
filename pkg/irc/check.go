package irc

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/Someblueman/airc/internal/protocol"
)

// RequestCheck has the same single outstanding request rule as RequestChat.
// Unrelated live events are forwarded while the complete snapshot is read.
func RequestCheck(ctx context.Context, c *Client, r CheckRequest, other func(Event)) ([]CheckEntry, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	line := "CHECK :" + string(data)
	if len(line) > protocol.MaxLineLength {
		return nil, errors.New("combined check exceeds wire limit; select fewer targets")
	}
	if err := c.Raw(line); err != nil {
		return nil, err
	}
	var entries []CheckEntry
	for {
		select {
		case event, ok := <-c.Events():
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, io.EOF
			}
			if raw, ok := event.(*RawEvent); ok {
				switch raw.Command {
				case "784":
					var entry CheckEntry
					if err := json.Unmarshal([]byte(raw.Trailing), &entry); err != nil {
						return nil, err
					}
					if len(entries) >= maxResponseEntries {
						return nil, errors.New("server exceeded combined check response limit")
					}
					entries = append(entries, entry)
					continue
				case "785":
					return entries, nil
				}
				if err := raw.Rejection(); err != nil {
					return nil, err
				}
			}
			if other != nil {
				other(event)
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
