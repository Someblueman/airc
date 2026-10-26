package adapter

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"

	"github.com/Someblueman/airc/internal/testwait"
)

func TestCoAPReadyGateFollowsSuccessfulCarrierWrite(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	token := message.Token("12345678")
	watch := &coapWatch{ready: make(chan struct{}), mid: -1}
	state := &coapState{active: map[string]*coapWatch{string(token): watch}, authenticated: map[string]bool{string(token): true}}
	var renewed atomic.Int32
	session := &coapSession{conn: server, writes: make(chan struct{}, 1), state: state, activity: func() { renewed.Add(1) }}
	msg := pool.NewMessage(t.Context())
	msg.SetToken(token)
	msg.SetType(message.Acknowledgement)
	msg.SetMessageID(7)
	msg.SetCode(codes.Content)
	msg.SetObserve(0)
	done := make(chan error, 1)
	go func() { done <- session.WriteMessage(msg) }()
	// net.Pipe has no buffering: until the client receives, output cannot succeed.
	select {
	case <-watch.ready:
		t.Fatal("ready gate opened before output was consumed")
	case <-time.After(20 * time.Millisecond):
	}
	if renewed.Load() != 0 {
		t.Fatal("activity renewed before successful output")
	}
	if err := client.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	wire := make([]byte, 1200)
	if n, err := client.Read(wire); err != nil || n < 4 {
		t.Fatalf("carrier read n=%d err=%v", n, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	select {
	case <-watch.ready:
	default:
		t.Fatal("successful ready write did not open watch gate")
	}
	if renewed.Load() != 1 {
		t.Fatal("successful authenticated output did not renew activity")
	}
	// A new partial request can reuse this token before authenticating its body.
	// Its transport Continue response must not inherit authenticated renewal.
	partial := pool.NewMessage(t.Context())
	partial.SetToken(token)
	partial.SetType(message.Acknowledgement)
	partial.SetMessageID(8)
	partial.SetCode(codes.Continue)
	go func() { done <- session.WriteMessage(partial) }()
	if _, err := client.Read(wire); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if renewed.Load() != 1 {
		t.Fatal("partial unauthenticated Block1 acknowledgement renewed activity")
	}
}

func TestCoAPWriteDeadlineIncludesContendedAdmission(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	session := &coapSession{conn: server, writes: make(chan struct{}, 1), state: &coapState{active: make(map[string]*coapWatch), authenticated: make(map[string]bool)}, activity: func() { t.Error("blocked write renewed activity") }}
	firstCtx, finish := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer finish()
	first := pool.NewMessage(firstCtx)
	first.SetType(message.Confirmable)
	first.SetMessageID(1)
	first.SetCode(codes.Content)
	done := make(chan error, 1)
	go func() { done <- session.WriteMessage(first) }()
	testwait.Until(t, time.Second, "the first writer to acquire admission", func() bool { return len(session.writes) > 0 })
	secondCtx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	second := pool.NewMessage(secondCtx)
	second.SetType(message.Confirmable)
	second.SetMessageID(2)
	second.SetCode(codes.Content)
	started := time.Now()
	if err := session.WriteMessage(second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("contended write error=%v", err)
	}
	if time.Since(started) > 80*time.Millisecond {
		t.Fatal("second writer waited for first writer beyond its deadline")
	}
	if err := <-done; err == nil {
		t.Fatal("blocked first carrier write succeeded")
	}
}
