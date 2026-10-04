package server

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func soakAgent(ctx context.Context, address, root string, cycle, index int) error {
	c, err := irc.DialContext(ctx, irc.Config{Addr: address, Nick: fmt.Sprintf("soak%d", index), Ephemeral: true})
	if err != nil {
		return err
	}
	defer c.Close()
	body := "@soak0 " + strings.Repeat("x", 4080)
	for j := range 20 {
		if _, err := soakSend(ctx, c, root, fmt.Sprintf("c%d-a%d-m%d", cycle, index, j), body); err != nil {
			return err
		}
	}
	return nil
}

func soakSend(ctx context.Context, c *irc.Client, parent, key, body string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var err error
	if parent == "" {
		err = c.SendWithID("#soak", body, key)
	} else {
		err = c.ReplyWithID(parent, body, key)
	}
	if err != nil {
		return "", err
	}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case e, ok := <-c.Events():
			if !ok {
				return "", io.EOF
			}
			if receipt, ok := e.(*irc.SendReceiptEvent); ok && receipt.RequestID == key {
				return receipt.ID, nil
			}
			if raw, ok := e.(*irc.RawEvent); ok && len(raw.Command) == 3 && raw.Command[0] >= '4' && raw.Command[0] <= '5' && raw.Command != "422" {
				return "", fmt.Errorf("server rejected %s: %s", raw.Command, raw.Trailing)
			}
		}
	}
}
