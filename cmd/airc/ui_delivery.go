package main

import (
	"context"
	"errors"
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
		m.uncertainAction = uncertainChat(&result.cmd) && !rejectedSend(result.err)
		if result.uncertainID != "" || result.cmd.kind != "retry-send" {
			m.uncertainID = result.uncertainID
		}
		m.setStatus(result.err.Error(), true)
		return
	}
	m.uncertainAction = false
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
	if !c.Supports("SAFE_RETRY") {
		return "", errors.New("UI recovery requires SAFE_RETRY; draft kept"), ""
	}
	box, err := openOutbox(b.opt)
	if err != nil {
		return "", err, cmd.requestID
	}
	defer box.close()
	var entry *outboundMessage
	if cmd.kind == "retry-send" {
		entry = box.find(cmd.target)
		if entry == nil {
			return "", errors.New("saved request not found; inspect airc send --pending"), cmd.target
		}
	} else {
		target, reply := cmd.target, ""
		if cmd.kind == "reply" {
			target, reply = "reply:"+cmd.target, cmd.target
		}
		entry, err = box.add(target, reply, cmd.text, cmd.requestID)
		if err != nil {
			return "", err, cmd.requestID
		}
	}
	if entry.Result == nil {
		if cmd.kind == "retry-send" {
			err = c.RetryRequest(entry.RequestID)
		} else if entry.ReplyTo != "" {
			err = c.ReplyWithID(entry.ReplyTo, entry.Body, entry.RequestID)
		} else {
			err = c.SendWithID(entry.Target, entry.Body, entry.RequestID)
		}
		attempted := err == nil
		if err == nil {
			entry.Result, err = awaitSend(ctx, c, b.nick, entry.Target, entry.Body, entry.ReplyTo, "", entry.RequestID, translate)
		}
		if err != nil {
			if cmd.kind != "retry-send" && (rejectedSend(err) || !attempted && !failure(err, "send").Retryable) {
				id := entry.RequestID
				for i := range box.Entries {
					if box.Entries[i].RequestID == id {
						box.Entries = append(box.Entries[:i], box.Entries[i+1:]...)
						break
					}
				}
				if saveErr := box.save(); saveErr != nil {
					return "", saveErr, id
				}
				return "", err, ""
			}
			return "", uncertainSend(err, entry.RequestID), entry.RequestID
		}
		if err := box.save(); err != nil {
			return "", acceptedFailure(err, entry.Result), entry.RequestID
		}
	}
	r := entry.Result
	receipt := &irc.SendReceiptEvent{Receipt: r.Receipt, ID: r.ID, From: r.From, Target: r.Target}
	if r.Delivered != nil {
		receipt.Queued = !*r.Delivered
	}
	return receiptStatus(receipt), nil, ""

}

// The UI is the only producer. Reserve the whole batch before enqueuing so a
// busy backend cannot accept half a slash command and discard its input text.
func queueUICommands(m *uiModel, queue chan<- uiCmd, wanted []uiCmd, draft string, cursor int) {
	if cap(queue)-len(queue) < len(wanted) {
		err := errors.New("busy; draft kept, try again")
		for _, cmd := range wanted {
			m.delivery(deliveryIn{cmd: cmd, err: err})
		}
		// Nothing entered the backend, so even a chat mutation is safe to edit.
		m.uncertainAction = false
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
