package fallback

import (
	"errors"
	"testing"
)

// Follows through a real gateway (framed and WebSocket streams, the lease,
// the connection idle) are tested in the gateway module, which runs one in
// process: gateway/integration, client_follow_test.go.

// A one-shot HTTP carrier has no stream: Follow says so at once, so a
// caller polls with CheckAll instead.
func TestFollowIsRefusedOnARequestCarrier(t *testing.T) {
	g := newFakeGateway(t)
	c := &Client{Host: "127.0.0.1", Nick: "alice", Endpoints: []Endpoint{g.endpoint(t)}}
	reader, err := c.ReadRooms(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if reader.Follows() {
		t.Fatal("an HTTP carrier claims to follow")
	}
	if err := reader.Follow(t.Context(), CheckSet{Rooms: []RoomAfter{{Room: "#room"}}, Limit: 10}, 0, func(Checked) error { return nil }); !errors.Is(err, ErrNoFollow) {
		t.Fatalf("Follow on HTTP: %v", err)
	}
}
