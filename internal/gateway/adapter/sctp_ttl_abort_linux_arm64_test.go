//go:build linux && arm64

package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"golang.org/x/sys/unix"
)

func TestSCTPOrderedTTLPartialAbortNeverReturnsPrefixOrIntakesRequest(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrderedTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverListener := listener.(*sctpListener)
	setEndpoint := func(fd uintptr) error {
		return unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 4096)
	}
	var endpointErr error
	if err := serverListener.raw.Control(func(fd uintptr) { endpointErr = setEndpoint(fd) }); err != nil {
		t.Fatal(err)
	}
	if endpointErr != nil {
		t.Fatal(endpointErr)
	}
	client := sctpTestClientWithTTL(t, listener.Addr(), 0, 512)
	defer client.close()
	accepted := acceptSCTPTestConn(t, listener).(*sctpConn)
	defer accepted.Close()
	if err := accepted.raw.Control(func(fd uintptr) {
		endpointErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, 4096)
		if endpointErr == nil {
			endpointErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_SCTP, 19, 512)
		}
	}); err != nil {
		t.Fatal(err)
	}
	if endpointErr != nil {
		t.Fatal(endpointErr)
	}
	accepted.raw = &sctpPauseRawConn{RawConn: accepted.raw, pause: 1250 * time.Millisecond}

	f, request, protection, _ := sctpApplicationFixture(t)
	wire, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) >= sctpRecordLimit {
		t.Fatalf("encrypted frame already exceeds one record: %d", len(wire))
	}
	wire = append(wire, bytes.Repeat([]byte{' '}, sctpRecordLimit-len(wire))...)
	if len(wire) != sctpRecordLimit {
		t.Fatalf("padded request bytes=%d, want %d", len(wire), sctpRecordLimit)
	}
	slots := make(chan struct{}, 1)
	writing := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		done <- serveSCTPConnection(ctx, &sctpObservedWriter{Conn: accepted, writing: writing}, f.d, slots, protection, SCTPOrderedTTL)
	}()
	defer func() {
		cancel()
		_ = accepted.Close()
		select {
		case <-finished:
		case <-time.After(2 * time.Second):
			t.Error("aborted SCTP application handler did not join")
		}
	}()
	client.sendTTL(t, wire)
	select {
	case err := <-done:
		if !errors.Is(err, errSCTPInvalidRecord) {
			t.Fatalf("partial delivery ended with %v, want invalid-record refusal", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partial delivery abort did not terminate the connection")
	}
	if len(slots) != 0 {
		t.Fatalf("application admission slots after partial abort=%d", len(slots))
	}
	if _, err := f.s.Lookup(t.Context(), f.identity, amqpRequest().RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("partial request reached durable intake: %v", err)
	}
	select {
	case <-writing:
		t.Fatal("application wrote a response for the incomplete request")
	default:
	}
}
