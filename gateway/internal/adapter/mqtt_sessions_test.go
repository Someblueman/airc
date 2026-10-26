package adapter

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestMQTTReconnectJoinsAndCannotReleaseSuccessor(t *testing.T) {
	o := mqttSessions{active: make(map[string]*mqttSession)}
	newSession := func() (*mqttSession, context.Context) {
		ctx, cancel := context.WithCancel(t.Context())
		server, client := net.Pipe()
		t.Cleanup(func() { cancel(); _ = server.Close(); _ = client.Close() })
		return &mqttSession{conn: server, cancel: cancel, done: make(chan struct{})}, ctx
	}
	first, firstCtx := newSession()
	second, secondCtx := newSession()
	if o.claim(firstCtx, "publisher", "", first) != nil {
		t.Fatal("initial claim")
	}
	result := make(chan error, 1)
	go func() { result <- o.claim(secondCtx, "publisher", "", second) }()
	select {
	case <-firstCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("predecessor not cancelled")
	}
	select {
	case <-result:
		t.Fatal("replacement acknowledged before predecessor joined")
	default:
	}
	o.release("publisher", first)
	close(first.done)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal("replacement refused")
		}
	case <-time.After(time.Second):
		t.Fatal("replacement not joined")
	}
	o.mu.Lock()
	current := o.active["publisher"]
	o.mu.Unlock()
	if current != second {
		t.Fatal("old release removed successor")
	}
	o.release("publisher", second)
	if len(o.active) != 0 {
		t.Fatal("leaked publisher")
	}
}

func TestMQTTReplacementCancellationAndSeparateClients(t *testing.T) {
	o := mqttSessions{active: make(map[string]*mqttSession)}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	old := &mqttSession{conn: server, cancel: func() {}, done: make(chan struct{})}
	o.active["publisher"] = old
	ctx, cancel := context.WithCancel(t.Context())
	other := &mqttSession{}
	if o.claim(ctx, "other", "", other) != nil {
		t.Fatal("separate client refused")
	}
	next := &mqttSession{}
	cancel()
	if o.claim(ctx, "publisher", "", next) == nil {
		t.Fatal("cancelled replacement accepted")
	}
	o.release("publisher", old)
	if o.active["publisher"] != next || o.active["other"] != other {
		t.Fatal("release crossed authority")
	}
	o.release("publisher", next)
	o.release("other", other)
}

// An identified connection is replaced only by its own author: another
// author, or a connection without one, is refused and changes nothing.
func TestMQTTClaimKeepsAnAuthorsClientID(t *testing.T) {
	o := mqttSessions{active: make(map[string]*mqttSession)}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	cancelled := false
	victim := &mqttSession{conn: server, cancel: func() { cancelled = true }, done: make(chan struct{})}
	if err := o.claim(t.Context(), "device-7", "a:alice", victim); err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"a:mallory", "g:alice", ""} {
		if err := o.claim(t.Context(), "device-7", owner, &mqttSession{}); !errors.Is(err, errMQTTClientIDHeld) {
			t.Fatalf("claim as %q: %v", owner, err)
		}
	}
	if cancelled || o.active["device-7"] != victim {
		t.Fatal("a refused claim disturbed the holder")
	}
	// A connection that identifies later is protected from then on.
	anonymous := &mqttSession{}
	if err := o.claim(t.Context(), "device-8", "", anonymous); err != nil {
		t.Fatal(err)
	}
	o.identify(anonymous, "a:bob")
	if err := o.claim(t.Context(), "device-8", "", &mqttSession{}); !errors.Is(err, errMQTTClientIDHeld) {
		t.Fatalf("claim after identify: %v", err)
	}
	close(victim.done)
}
