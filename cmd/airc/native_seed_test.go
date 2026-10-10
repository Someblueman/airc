package main

import (
	"context"
	"testing"
	"time"
)

type seedMessage struct {
	target string
	body   string
}

// Bulk fixture history crosses the real native protocol, with each acceptance
// confirmed before the CLI under test runs. No CLI state or output is seeded.
func seedNativeMessages(t *testing.T, address, nick string, messages []seedMessage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	client, err := dialOneShot(ctx, options{nick: nick, addr: address})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, message := range messages {
		if err := client.Send(message.target, message.body); err != nil {
			t.Fatal(err)
		}
		if _, err := awaitSend(ctx, client, nick, message.target, message.body, "", "", "", nil); err != nil {
			t.Fatal(err)
		}
	}
}
