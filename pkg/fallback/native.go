package fallback

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// dialNative opens an ephemeral session: it claims no nickname and leaves no
// join or quit traffic, like the CLI's one-shot commands. The connection is
// established once the server has registered it. On a gateway tunnel the
// stream is the tunnel's; TLS, when the tunnel has it, is the tunnel's too,
// so pkg/irc sees a plain stream carrying aircd's protocol.
func (c *Client) dialNative(ctx context.Context, e Endpoint, progress func()) (*irc.Client, error) {
	network, address := c.address(e)
	cfg := irc.Config{Nick: c.Nick, Network: network, Addr: address, IdentityToken: c.Token, AccessToken: c.Password, Ephemeral: true, KeepAlive: c.KeepAlive, ProbeTimeout: c.ProbeTimeout}
	switch e.Name {
	case "irc_websocket":
		cfg.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dialIRCWebSocket(ctx, e, progress) }
	case "irc_connect":
		cfg.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) { return c.dialIRCConnect(ctx, e, progress) }
	default:
		cfg.TLSConfig = c.tlsConfig(e)
		cfg.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			return c.dial(ctx, network, address, progress)
		}
	}
	return irc.DialContext(ctx, cfg)
}

// native is k's IRC client, closed if ctx ends while an operation runs on it.
func native(ctx context.Context, k *Conn) (*irc.Client, func() bool) {
	client := k.native
	return client, context.AfterFunc(ctx, func() { _ = client.Close() })
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
	// A server that has gone silent fails in about a second, so the next
	// carrier can answer instead.
	defer client.ProbeAfter(irc.AnswerWait, irc.AnswerProbeTimeout)()
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
		r.RecipientConnected, r.RecipientActive = event.Receipt.RecipientConnected, event.Receipt.RecipientActive
	}
	if r.RecipientConnected == nil && !protocol.IsChannel(event.Target) {
		connected := !event.Queued
		r.RecipientConnected = &connected
	}
	return r
}

func (c *Client) nativePost(ctx context.Context, k *Conn, m Message) (*Receipt, error) {
	client, stop := native(ctx, k)
	defer stop()
	var err error
	options := irc.SendOptions{RequestID: m.RequestID, Run: m.Run}
	if m.ParentID != "" {
		err = client.ReplyWith(m.ParentID, m.Body, options)
	} else {
		err = client.SendWith(m.Target, m.Body, options)
	}
	if err != nil {
		if !client.Supports("IDEMPOTENCY") {
			return nil, err // refused locally before any write
		}
		return nil, &uncertain{err}
	}
	receipt, err := awaitNative(ctx, client, func(event irc.Event) (*Receipt, bool) {
		if r, ok := event.(*irc.SendReceiptEvent); ok && r.RequestID == m.RequestID {
			return receiptOf(k.Endpoint, r), true
		}
		return nil, false
	})
	if err != nil && !Definite(err) {
		return nil, &uncertain{err}
	}
	return receipt, err
}

// nativeReact adds a reaction. Native IRC has no request ID for it; AIRC
// answers a repeated reaction from the same author with the existing one.
func (c *Client) nativeReact(ctx context.Context, k *Conn, r Reaction) (*Receipt, error) {
	client, stop := native(ctx, k)
	defer stop()
	if err := client.React(r.ParentID, r.Symbol); err != nil {
		return nil, err
	}
	receipt, err := awaitNative(ctx, client, func(event irc.Event) (*Receipt, bool) {
		var msg *irc.MessageEvent
		var receipt *Receipt
		switch v := event.(type) {
		case *irc.SendReceiptEvent:
			msg, receipt = v.MessageEvent(), receiptOf(k.Endpoint, v)
		case *irc.MessageEvent:
			msg, receipt = v, &Receipt{Endpoint: k.Endpoint, MessageID: v.ID}
		}
		if msg == nil || msg.Reaction != r.Symbol || msg.ReplyTo != r.ParentID || !strings.EqualFold(msg.From, client.Nick()) {
			return nil, false
		}
		receipt.RequestID = r.RequestID
		return receipt, true
	})
	if err != nil && !Definite(err) {
		return nil, &uncertain{err}
	}
	return receipt, err
}

func (c *Client) nativeLookup(ctx context.Context, k *Conn, requestID string) (*Receipt, error) {
	client, stop := native(ctx, k)
	defer stop()
	if err := client.RetryRequest(requestID); err != nil {
		return nil, err
	}
	return awaitNative(ctx, client, func(event irc.Event) (*Receipt, bool) {
		if r, ok := event.(*irc.SendReceiptEvent); ok && r.RequestID == requestID {
			return receiptOf(k.Endpoint, r), true
		}
		return nil, false
	})
}

func (c *Client) nativeHistory(ctx context.Context, k *Conn, room string, limit int) (*Page, error) {
	client, stop := native(ctx, k)
	defer stop()
	// An ephemeral session reads without joining.
	if err := client.HistoryAfter(room, "", limit); err != nil {
		return nil, err
	}
	return awaitPage(ctx, client, k, room)
}

// nativeSearch reads the oldest retained matches in a room.
func (c *Client) nativeSearch(ctx context.Context, k *Conn, s Search) (*Page, error) {
	client, stop := native(ctx, k)
	defer stop()
	if err := client.Search(s.Room, s.Query, s.From, "*", s.Limit); err != nil {
		return nil, err
	}
	return awaitPage(ctx, client, k, s.Room)
}

// awaitPage collects a room's history or search page.
func awaitPage(ctx context.Context, client *irc.Client, k *Conn, room string) (*Page, error) {
	page := &Page{Endpoint: k.Endpoint, Messages: []*irc.MessageMetadata{}}
	_, err := awaitNative(ctx, client, func(event irc.Event) (struct{}, bool) {
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

func (c *Client) nativeChannels(ctx context.Context, k *Conn) ([]irc.ChannelInfo, error) {
	client, stop := native(ctx, k)
	defer stop()
	if err := client.Channels(); err != nil {
		return nil, err
	}
	var list []irc.ChannelInfo
	_, err := awaitNative(ctx, client, func(event irc.Event) (struct{}, bool) {
		switch v := event.(type) {
		case *irc.ChannelEvent:
			list = append(list, v.Channel)
		case *irc.EndOfChannelsEvent:
			return struct{}{}, true
		}
		return struct{}{}, false
	})
	return list, err
}

func (c *Client) nativeDirectory(ctx context.Context, k *Conn, nick string) ([]irc.AgentCard, error) {
	client, stop := native(ctx, k)
	defer stop()
	if err := client.Directory(nick); err != nil {
		return nil, err
	}
	cards := []irc.AgentCard{}
	_, err := awaitNative(ctx, client, func(event irc.Event) (struct{}, bool) {
		switch v := event.(type) {
		case *irc.DirectoryEvent:
			cards = append(cards, v.AgentCard)
		case *irc.EndOfDirectoryEvent:
			return struct{}{}, true
		}
		return struct{}{}, false
	})
	return cards, err
}
