//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func waitIRCNativeAccepted(t *testing.T, f *fileMailboxFixture, id, messageID string) {
	t.Helper()
	conn, err := net.DialTimeout("unix", f.config.Listeners[0].Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(5 * time.Second)
	if err := conn.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		request := core.Request{Version: 1, RequestID: "irc-receipt-" + id, Operation: core.ReceiptGet, LookupID: ircNativeRequestID(id)}
		frame := adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)}
		writeFrame(t, ircReceiptDeadlineConn{Conn: conn, deadline: deadline}, frame)
		response := readFrame(t, ircReceiptDeadlineConn{Conn: conn, deadline: deadline})
		if !time.Now().Before(deadline) {
			t.Fatal("IRC receipt exceeded absolute deadline", id)
		}
		if response.CorrelationID != frame.CorrelationID || response.Result == nil || response.Result.Result == nil {
			t.Fatal("IRC receipt response", response)
		}
		receipt := response.Result.Result
		if receipt.State == core.Accepted {
			if !receipt.Durable || receipt.MessageID != messageID || receipt.SendAttempts != 1 {
				t.Fatal("IRC accepted receipt", receipt)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("IRC native observation did not reach persisted accepted receipt", id)
}

func configureIRCReceiptListener(t *testing.T, f *fileMailboxFixture) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "irc-receipt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f.config.Listeners = []config.Listener{{ID: "receipt", Protocol: "unix", Address: filepath.Join(root, "r.sock"), Enabled: true, Operations: []core.Operation{core.ReceiptGet}}}
}

// Keep shared framing helpers within this receipt control's absolute deadline.
type ircReceiptDeadlineConn struct {
	net.Conn
	deadline time.Time
}

func (c ircReceiptDeadlineConn) SetReadDeadline(value time.Time) error {
	if value.IsZero() || value.After(c.deadline) {
		value = c.deadline
	}
	return c.Conn.SetReadDeadline(value)
}
func (c ircReceiptDeadlineConn) SetWriteDeadline(value time.Time) error {
	if value.IsZero() || value.After(c.deadline) {
		value = c.deadline
	}
	return c.Conn.SetWriteDeadline(value)
}
