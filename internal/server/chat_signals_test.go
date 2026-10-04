package server

import (
	"fmt"
	"github.com/Someblueman/airc/internal/protocol"
	"testing"
	"time"
)

func TestActivitySignalsStayBoundedAndPruneExpiredState(t *testing.T) {
	s := New(Config{HistoryLimit: 8})
	actor := &session{client: Client{Nick: "writer"}, out: make(chan string, 1), done: make(chan struct{})}
	for i := range 1024 {
		if err := s.signalLocked(actor, protocol.ChatRequest{Action: "typing", Target: fmt.Sprintf("#r%d", i), Seconds: 15}); err != nil {
			t.Fatal(err)
		}
		<-actor.out
	}
	if err := s.signalLocked(actor, protocol.ChatRequest{Action: "typing", Target: "#overflow", Seconds: 15}); err == nil {
		t.Fatal("unbounded signal growth")
	}
	for key, entry := range s.signals {
		entry.ExpiresAt = time.Now().Add(-time.Second)
		s.signals[key] = entry
	}
	if err := s.signalLocked(actor, protocol.ChatRequest{Action: "thinking", Target: "#new", Seconds: 10, Text: "working"}); err != nil {
		t.Fatal(err)
	}
	<-actor.out
	if len(s.signals) != 1 || len(s.signalTimes) != 1 || s.history.size != 0 {
		t.Fatal("activity retained stale state or polluted history")
	}
	key := "#new::guest:writer"
	expiry := s.signals[key].ExpiresAt
	if err := s.signalLocked(actor, protocol.ChatRequest{Action: "thinking", Target: "#new", Seconds: 15, Text: "different"}); err != nil {
		t.Fatal(err)
	}
	<-actor.out
	if !s.signals[key].ExpiresAt.Equal(expiry) || s.signals[key].Text != "working" {
		t.Fatal("signal throttle bypassed")
	}
}
