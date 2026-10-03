package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestOutboxBoundsPreserveUncertainEntries(t *testing.T) {
	agentEnv(t)
	b, err := openOutbox(options{addr: "127.0.0.1:1234", nick: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	for i := 0; i < maxOutboxEntries; i++ {
		b.Entries = append(b.Entries, outboundMessage{RequestID: fmt.Sprint(i), Target: "#room", Body: "pending"})
	}
	if _, err := b.add("#room", "", "new", "extra"); err == nil {
		t.Fatal("full uncertain outbox accepted a new send")
	}
	if len(b.Entries) != maxOutboxEntries || b.find("0") == nil {
		t.Fatal("uncertain entry was discarded")
	}
	b.Entries[20].Result = &sendResult{MessageEvent: &irc.MessageEvent{ID: "confirmed", ChatMetadata: irc.ChatMetadata{RequestID: "20"}, Message: "pending"}}
	if _, err := b.add("#room", "", "new", "extra"); err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != maxOutboxEntries || b.find("20") != nil || b.find("0") == nil || b.find("extra") == nil {
		t.Fatal("wrong entry was evicted")
	}
	info, err := os.Stat(b.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("outbox is not owner-only", err)
	}
	if _, err := b.add("#room", "", "different", "extra"); err == nil {
		t.Fatal("request ID content changed")
	}
}
