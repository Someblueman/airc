package adapter

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

type recordingPackets struct {
	net.PacketConn
	written int
}

func (r *recordingPackets) WriteTo(p []byte, _ net.Addr) (int, error) {
	r.written += len(p)
	return len(p), nil
}
func (r *recordingPackets) SetWriteDeadline(time.Time) error { return nil }

// A reply to an unverified source is bounded to three times the request's
// wire bytes (plus a small final-frame reserve); loopback is exempt.
func TestAuthorDatagramReplyAmplificationBound(t *testing.T) {
	u := &AuthorDatagrams{writeSlot: make(chan struct{}, 1)}
	remote := &net.UDPAddr{IP: net.ParseIP("192.0.2.7"), Port: 4000}
	big := FrameResponse{Version: 1, CorrelationID: "c", Event: "page", Reason: strings.Repeat("x", 2000)}
	conn := &recordingPackets{}
	write := u.writer(context.Background(), conn, remote, amplificationFactor*300)
	if err := write(big); err != store.ErrCapacity || conn.written != 0 {
		t.Fatalf("oversized reply to a remote source: %v written=%d", err, conn.written)
	}
	if err := write(FrameResponse{Version: 1, CorrelationID: "c", Event: "error", Error: &core.Failure{Code: core.CapacityExceeded}}); err != nil || conn.written == 0 {
		t.Fatalf("final error frame: %v", err)
	}
	if loopbackPacketAddr(remote) || !loopbackPacketAddr(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)}) || !loopbackPacketAddr(&ICMPv6Addr{IP: net.IPv6loopback}) {
		t.Fatal("loopback classification")
	}
	loopback := &recordingPackets{}
	if err := u.writer(context.Background(), loopback, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 4000}, -1)(big); err != nil || loopback.written < 2000 {
		t.Fatalf("loopback reply: %v %d", err, loopback.written)
	}
}
