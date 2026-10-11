package adapter

import (
	"context"
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
	if !o.claim(firstCtx, "publisher", first) {
		t.Fatal("initial claim")
	}
	result := make(chan bool, 1)
	go func() { result <- o.claim(secondCtx, "publisher", second) }()
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
	case ok := <-result:
		if !ok {
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
	if !o.claim(ctx, "other", other) {
		t.Fatal("separate client refused")
	}
	next := &mqttSession{}
	cancel()
	if o.claim(ctx, "publisher", next) {
		t.Fatal("cancelled replacement accepted")
	}
	o.release("publisher", old)
	if o.active["publisher"] != next || o.active["other"] != other {
		t.Fatal("release crossed authority")
	}
	o.release("publisher", next)
	o.release("other", other)
}
