//go:build darwin || linux

package adapter

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/natswire"
)

func TestNATSFixedSubscriptions(t *testing.T) {
	_, c, _ := natsConnectionFixture(t, false)
	if handled, err := c.control(natswire.Frame{Command: "PUB"}); handled || err != nil {
		t.Fatal("application command acknowledged")
	}
	if _, err := c.control(natswire.Frame{Command: "SUB", Subject: "other", SID: "1"}); err == nil {
		t.Fatal("ungranted subject")
	}
	if _, err := c.control(natswire.Frame{Command: "SUB", Subject: "airc.response", SID: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.control(natswire.Frame{Command: "SUB", Subject: "airc.response", SID: "2"}); err == nil {
		t.Fatal("second subscription")
	}
	if _, err := c.control(natswire.Frame{Command: "UNSUB", SID: "wrong"}); err == nil {
		t.Fatal("wrong subscription identity")
	}
	limit := uint32(2)
	if _, err := c.control(natswire.Frame{Command: "UNSUB", SID: "1", MaxMessages: &limit}); err != nil {
		t.Fatal(err)
	}
	limit = 100 // The control frame must not own the stored limit.
	c.sent()
	if c.subscription == nil || c.subscription.delivered != 1 {
		t.Fatal("removed early")
	}
	c.sent()
	if c.subscription != nil {
		t.Fatal("automatic unsubscribe missed")
	}
	for _, count := range []uint32{0, 1} {
		if _, err := c.control(natswire.Frame{Command: "SUB", Subject: "airc.response", SID: "1"}); err != nil {
			t.Fatal(err)
		}
		c.sent()
		if _, err := c.control(natswire.Frame{Command: "UNSUB", SID: "1", MaxMessages: &count}); err != nil {
			t.Fatal(err)
		}
		if c.subscription != nil {
			t.Fatal("completed limit retained")
		}
	}
	if _, err := c.control(natswire.Frame{Command: "SUB", Subject: "airc.response", SID: "1"}); err != nil {
		t.Fatal(err)
	}
	c.subscription.delivered = 1<<32 - 1
	c.sent()
	if c.subscription != nil {
		t.Fatal("delivery counter wrapped")
	}
}

func TestNATSSessionControlsAndUnsubscribedPublishRefusal(t *testing.T) {
	client, c, r := natsConnectionFixture(t, true)
	done := make(chan struct{})
	go func() { defer close(done); c.serveNATS("fixture", nil, nil, make(chan struct{}, 1)) }()
	for _, tc := range []struct{ wire, reply string }{
		{"SUB airc.response 1\r\n", "+OK"}, {"PING\r\n", "PONG"}, {"UNSUB 1 1\r\n", "+OK"}, {"UNSUB 1\r\n", "+OK"}, {"PUB airc.request airc.response 1\r\nx\r\n", "-ERR"},
	} {
		if _, err := client.Write([]byte(tc.wire)); err != nil {
			t.Fatal(err)
		}
		if f, err := natswire.Read(r); err != nil || f.Command != tc.reply {
			t.Fatalf("control reply %s: %s %v", tc.reply, f.Command, err)
		}
	}
	<-done
	c.close()
}
