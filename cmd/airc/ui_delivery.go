package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

type deliveryIn struct {
	cmd         uiCmd
	err         error
	uncertainID string
	acceptance  string
}

func confirmedUICmd(cmd uiCmd) bool {
	return cmd.kind == "send" || cmd.kind == "reply" || cmd.kind == "retry-send" || cmd.kind == "chat"
}

// Keep one submitted draft until its acknowledgement. Editing is paused while
// it is in flight, so a late result cannot erase a newer draft.
func (m *uiModel) delivery(result deliveryIn) {
	if m.pending == nil || *m.pending != result.cmd {
		return
	}
	m.pending = nil
	if result.err != nil {
		if result.uncertainID != "" || result.cmd.kind != "retry-send" {
			m.uncertainID = result.uncertainID
		}
		m.setStatus(result.err.Error(), true)
		return
	}
	m.input, m.cursor = nil, 0
	m.uncertainID = ""
	m.replyTo = ""
	text := result.acceptance
	if text == "" {
		text = "Accepted"
	}
	m.setStatus(text, false)
}

func (b *uiBackend) sendConfirmed(ctx context.Context, c *irc.Client, cmd uiCmd, translate func(irc.Event)) (status string, err error, uncertain string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !c.Supports("RECEIPTS") {
		return "", errors.New("sending from the UI requires daemon receipts; draft kept"), ""
	}
	id := "ui-" + rand.Text()
	switch cmd.kind {
	case "send":
		err = c.SendWithID(cmd.target, cmd.text, id)
	case "reply":
		err = c.ReplyWithID(cmd.target, cmd.text, id)
	case "retry-send":
		id = cmd.target
		err = c.RetryRequest(id)
	}
	if err != nil {
		var networkError net.Error
		if cmd.kind == "retry-send" || errors.As(err, &networkError) || errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
			return "", fmt.Errorf("%v; confirmation unknown (%s); Enter checks the receipt", err, id), id
		}
		return "", err, ""
	}
	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("Confirmation unknown (%s); Enter checks the receipt, Esc discards this draft", id), id
		case event, ok := <-c.Events():
			if !ok {
				return "", fmt.Errorf("%w; confirmation unknown (%s); Enter checks the receipt", io.EOF, id), id
			}
			if err := serverError(event); err != nil {
				if cmd.kind == "retry-send" {
					return "", fmt.Errorf("%v; original confirmation still unknown (%s); Enter checks again, Esc discards", err, id), id
				}
				return "", err, ""
			}
			translate(event)
			if receipt, ok := event.(*irc.SendReceiptEvent); ok && receipt.RequestID == id {
				return receiptStatus(receipt), nil, ""
			}
		}
	}
}

// The UI is the only producer. Reserve the whole batch before enqueuing so a
// busy backend cannot accept half a slash command and discard its input text.
func queueUICommands(m *uiModel, queue chan<- uiCmd, wanted []uiCmd, draft string, cursor int) {
	if cap(queue)-len(queue) < len(wanted) {
		err := errors.New("busy; draft kept, try again")
		for _, cmd := range wanted {
			m.delivery(deliveryIn{cmd: cmd, err: err})
		}
		if draft != "" && len(m.input) == 0 {
			m.input, m.cursor = []rune(draft), cursor
		}
		m.setStatus(err.Error(), true)
		return
	}
	for _, cmd := range wanted {
		queue <- cmd
	}
}

func receiptStatus(receipt *irc.SendReceiptEvent) string {
	text := "Accepted " + receipt.ID
	if receipt.Receipt != nil {
		if receipt.Receipt.Persisted {
			text += " · persisted"
		} else {
			text += " · not persisted"
		}
	}
	if !isChannel(receipt.Target) {
		if receipt.Queued {
			text += " · queued for " + receipt.Target
		} else {
			text += " · recipient connected"
		}
	}
	return text
}
