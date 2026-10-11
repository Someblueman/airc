//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testfs"
)

// waitIRCNativeAccepted reads the bridged guest's receipt over a plain Unix
// frame, as that guest: a guest receipt needs only the nickname.
func waitIRCNativeAccepted(t *testing.T, f *fileMailboxFixture, id, messageID string) {
	t.Helper()
	conn, err := net.DialTimeout("unix", f.config.Listeners[0].Address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frames := &frameConn{stream: conn}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response := frames.call(t, "irc-receipt-"+id, map[string]any{"version": 1, "request_id": "irc-receipt-" + id, "operation": "receipt.get", "lookup_id": ircNativeRequestID(id), "author": map[string]string{"nick": "irc-alice"}})
		if response.Result == nil || response.Result.Result == nil {
			t.Fatal("IRC receipt response", response.Error)
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
	root, err := testfs.MkdirTemp("irc-receipt-")
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
