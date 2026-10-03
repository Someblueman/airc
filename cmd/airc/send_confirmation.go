package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

func awaitSend(ctx context.Context, client *irc.Client, nick, target, body, reply, reaction, requestID string) (*sendResult, error) {
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, io.EOF
			}
			if err := serverError(event); err != nil {
				return nil, err
			}
			var msg *irc.MessageEvent
			var info *protocol.ReceiptInfo
			queued := false
			switch e := event.(type) {
			case *irc.MessageEvent:
				if client.Supports("RECEIPTS") && client.Ephemeral() {
					continue
				}
				msg = e
			case *irc.SendReceiptEvent:
				msg, queued, info = e.MessageEvent(), e.Queued, e.Receipt
			}
			if msg == nil || !strings.EqualFold(msg.From, nick) || msg.Message != body || msg.Reaction != reaction || requestID != "" && msg.RequestID != requestID {
				continue
			}
			if reply != "" && msg.ReplyTo != reply || reply == "" && !sameTarget(msg.Target, target) {
				continue
			}
			r := &sendResult{MessageEvent: msg, Receipt: info, Code: "accepted", Retryable: false}
			if info != nil && !info.Persisted {
				r.Code = "accepted_not_persisted"
			}
			if !isChannel(msg.Target) {
				delivered := !queued
				r.Delivered = &delivered
			}
			return r, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func recoverSend(ctx context.Context, opt options, entry *outboundMessage) (*sendResult, *irc.Client, error) {
	c, err := dialOneShot(ctx, opt)
	if err != nil {
		return nil, nil, err
	}
	if err = c.RetryRequest(entry.RequestID); err != nil {
		c.Close()
		return nil, nil, err
	}
	r, err := awaitSend(ctx, c, opt.nick, entry.Target, entry.Body, entry.ReplyTo, "", entry.RequestID)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	return r, c, nil
}

func uncertainSend(err error, id string) error {
	e := failure(err, "confirmation")
	e.RequestID = id
	if e.Code == "invalid_request" || e.Code == "server_unavailable" || e.Code == "timeout" {
		e.Code = "confirmation_unknown"
		e.Retryable = true
	}
	e.Message = fmt.Sprintf("%v; recover this send with airc send --retry %s using the same server and nickname; do not send the body again", err, id)
	return e
}

// Leave time inside the command deadline for one receipt-only reconnect.
func sendAttemptContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, 3*time.Second)
}

func rejectedSend(err error) bool {
	var rejection *irc.RejectedError
	return errors.As(err, &rejection)
}
