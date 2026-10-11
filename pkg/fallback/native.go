package fallback

import (
	"context"
	"fmt"
	"io"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// dialNative opens an ephemeral session: it claims no nickname and leaves no
// join or quit traffic, like the CLI's one-shot commands.
func (c *Client) dialNative(ctx context.Context, e Endpoint) (*irc.Client, error) {
	network, address := c.address(e)
	cfg := irc.Config{Nick: c.Nick, Network: network, Addr: address, TLSConfig: c.tlsConfig(e), IdentityToken: c.Token, AccessToken: c.Password, Ephemeral: true}
	dialCtx, cancel := context.WithTimeout(ctx, c.connectTimeout())
	defer cancel()
	client, err := irc.DialContext(dialCtx, cfg)
	if err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = client.Close() })
	go func() {
		<-client.Done()
		stop()
	}()
	return client, nil
}

// nativeRefused maps the error numerics a one-shot command can receive to
// definite refusals; it ignores everything else.
func nativeRefused(event irc.Event) error {
	raw, ok := event.(*irc.RawEvent)
	if !ok || len(raw.Command) != 3 || raw.Command[0] != '4' || raw.Command == "422" {
		return nil
	}
	return &irc.RejectedError{Code: raw.Command, Message: raw.Trailing}
}

// awaitNative waits for match to accept an event, a refusal, or the end.
func awaitNative[T any](ctx context.Context, client *irc.Client, match func(irc.Event) (T, bool)) (T, error) {
	var zero T
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				if ctx.Err() != nil {
					return zero, ctx.Err()
				}
				return zero, fmt.Errorf("server closed the connection: %w", io.ErrUnexpectedEOF)
			}
			if value, done := match(event); done {
				return value, nil
			}
			if err := nativeRefused(event); err != nil {
				return zero, err
			}
		case <-ctx.Done():
			return zero, ctx.Err()
		}
	}
}

func receiptOf(e Endpoint, event *irc.SendReceiptEvent) *Receipt {
	r := &Receipt{Endpoint: e, RequestID: event.RequestID, MessageID: event.ID}
	if event.Receipt != nil {
		persisted := event.Receipt.Persisted
		r.Persisted = &persisted
		r.RecipientConnected = event.Receipt.RecipientConnected
	}
	if r.RecipientConnected == nil && !protocol.IsChannel(event.Target) {
		connected := !event.Queued
		r.RecipientConnected = &connected
	}
	return r
}

func (c *Client) nativePost(ctx context.Context, e Endpoint, m Message) (*Receipt, error) {
	client, err := c.dialNative(ctx, e)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if m.ParentID != "" {
		err = client.ReplyWithID(m.ParentID, m.Body, m.RequestID)
	} else {
		err = client.SendWithID(m.Target, m.Body, m.RequestID)
	}
	if err != nil {
		if !client.Supports("IDEMPOTENCY") {
			return nil, err // refused locally before any write
		}
		return nil, &uncertain{err}
	}
	receipt, err := awaitNative(ctx, client, func(event irc.Event) (*Receipt, bool) {
		if r, ok := event.(*irc.SendReceiptEvent); ok && r.RequestID == m.RequestID {
			return receiptOf(e, r), true
		}
		return nil, false
	})
	if err != nil && !Definite(err) {
		return nil, &uncertain{err}
	}
	return receipt, err
}

func (c *Client) nativeLookup(ctx context.Context, e Endpoint, requestID string) (*Receipt, error) {
	client, err := c.dialNative(ctx, e)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if err := client.RetryRequest(requestID); err != nil {
		return nil, err
	}
	return awaitNative(ctx, client, func(event irc.Event) (*Receipt, bool) {
		if r, ok := event.(*irc.SendReceiptEvent); ok && r.RequestID == requestID {
			return receiptOf(e, r), true
		}
		return nil, false
	})
}

func (c *Client) nativeHistory(ctx context.Context, e Endpoint, room string, limit int) (*Page, error) {
	client, err := c.dialNative(ctx, e)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	if !client.Ephemeral() {
		if err := client.Join(room); err != nil {
			return nil, err
		}
	}
	if err := client.HistoryAfter(room, "", limit); err != nil {
		return nil, err
	}
	page := &Page{Endpoint: e, Messages: []*irc.MessageMetadata{}}
	_, err = awaitNative(ctx, client, func(event irc.Event) (struct{}, bool) {
		switch v := event.(type) {
		case *irc.HistoryEvent:
			if protocol.IsChannel(v.Target) && v.Target == room {
				page.Messages = append(page.Messages, &irc.MessageMetadata{ChatMetadata: v.ChatMetadata, ID: v.ID, ReplyTo: v.ReplyTo, Reaction: v.Reaction, ThreadID: v.ThreadID, Seq: v.Seq, From: v.From, Target: v.Target, Message: v.Message, Timestamp: v.Timestamp})
			}
		case *irc.EndOfHistoryEvent:
			if v.Target == room {
				page.Status, page.Cursor = v.Status, v.Cursor
				return struct{}{}, true
			}
		}
		return struct{}{}, false
	})
	if err != nil {
		return nil, err
	}
	return page, nil
}

func (c *Client) nativeCarriers(ctx context.Context, e Endpoint) ([]protocol.Carrier, error) {
	client, err := c.dialNative(ctx, e)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return protocol.ParseCarriers(client.Features()["CARRIERS"])
}
