package fallback

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/protocol"
)

// Gateways is a copy of c limited to its gateway carriers, for reads that
// continue where native IRC left off while it is unreachable.
func (c *Client) Gateways() *Client {
	gateways := *c
	gateways.Endpoints = nil
	for _, e := range c.Endpoints {
		if e.Name != "irc" {
			gateways.Endpoints = append(gateways.Endpoints, e)
		}
	}
	return &gateways
}

// RoomReader reads rooms after message IDs over one gateway connection, so a
// client can follow many rooms while native IRC is unreachable without
// holding a gateway watch per room.
type RoomReader struct {
	c *Client
	k *Conn
}

// ReadRooms races the client's gateway carriers (never native IRC) and reads
// over the first to connect.
func (c *Client) ReadRooms(ctx context.Context) (*RoomReader, error) {
	k, err := c.Gateways().Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &RoomReader{c: c, k: k}, nil
}

// ReadRooms reads rooms over k, a gateway connection, which the reader then
// owns.
func (k *Conn) ReadRooms() (*RoomReader, error) {
	if k.gateway == nil {
		return nil, errors.New("rooms are read this way only over a gateway carrier")
	}
	return &RoomReader{c: k.client, k: k}, nil
}

// Read returns up to limit of room's messages after a message ID (a native
// IRC cursor, or the last message seen), or its latest when after is "".
// Page.Gap reports an expired start. An error means the carrier stopped
// answering; read again on a new RoomReader.
func (r *RoomReader) Read(ctx context.Context, room, after string, limit int) (*Page, error) {
	if !protocol.IsChannel(room) || limit < 1 || limit > 1000 || after != "" && !protocol.ValidMessageID(after) {
		return nil, errors.New("a room read needs a room, a limit of 1-1000 and a message ID or nothing to start after")
	}
	start := after
	if start == "" {
		start = "recent"
	}
	page, err := r.c.gatewayRead(ctx, r.k, request{RequestID: "history", Operation: "history.read", Target: targetOf(room), Limit: limit, Start: start})
	if err != nil {
		return nil, err
	}
	for _, m := range page.Messages {
		if m == nil || !protocol.ValidMessageID(m.ID) || m.Target != room {
			return nil, errors.New("gateway answered a room read with a malformed message")
		}
	}
	return page, nil
}

// Endpoint is the carrier the reader reads over.
func (r *RoomReader) Endpoint() Endpoint { return r.k.Endpoint }

// Close closes the reader's connection.
func (r *RoomReader) Close() error { return r.k.Close() }
