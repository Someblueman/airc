//go:build linux

package gateway_test

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/internal/testfs"
)

type unixPacketFixture struct {
	*fileMailboxFixture
	process *gatewayProcess
	socket  string
}

func newUnixPacketFixture(t *testing.T) *unixPacketFixture {
	t.Helper()
	// Reuse native account/key/journal setup, never the mailbox transport.
	base := newFileMailboxFixture(t)
	root, err := testfs.MkdirTemp("up-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socketDirectory, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	f := &unixPacketFixture{fileMailboxFixture: base, socket: filepath.Join(socketDirectory, "g.sock")}
	f.config.Listeners[0].ID, f.config.Listeners[0].Protocol = "records", "unixpacket"
	f.config.Listeners[0].Address = f.socket
	// Records may target the fixture room and the author's direct target.
	f.config.Listeners[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	f.start(t)
	return f
}

func (f *unixPacketFixture) start(t *testing.T) {
	t.Helper()
	f.process = startGateway(t, f.binary, f.config)
	if address := gatewayAddress(t, f.process); address != f.socket {
		t.Fatal("incorrect record listener readiness", address)
	}
	if info, err := os.Lstat(f.socket); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("record socket is not owner-only", info, err)
	}
}

func (f *unixPacketFixture) stop(t *testing.T) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal("record gateway shutdown", f.process.err, f.process.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("record gateway shutdown did not join")
	}
	if _, err := os.Lstat(f.socket); !os.IsNotExist(err) {
		t.Fatal("record socket survived joined shutdown", err)
	}
}

func unixPacketDial(t *testing.T, f *unixPacketFixture) *net.UnixConn {
	t.Helper()
	conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Net: "unixpacket", Name: f.socket})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	return conn
}

func unixPacketWrite(t *testing.T, conn *net.UnixConn, body []byte) {
	t.Helper()
	if n, control, err := conn.WriteMsgUnix(body, nil, nil); err != nil || n != len(body) || control != 0 {
		t.Fatalf("whole request record: %d %d %v", n, control, err)
	}
}

func unixPacketRead(t *testing.T, conn *net.UnixConn) adapter.FrameResponse {
	t.Helper()
	body := make([]byte, adapter.MaxResponseFrameBytes)
	n, control, flags, _, err := conn.ReadMsgUnix(body, nil)
	if err != nil || n <= 0 || control != 0 || flags&(syscall.MSG_TRUNC|syscall.MSG_CTRUNC) != 0 {
		t.Fatalf("whole response record: %d %d %d %v", n, control, flags, err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(body[:n], &response); err != nil || response.Version != 1 {
		t.Fatal("invalid record response", response, err)
	}
	return response
}

func unixPacketCall(t *testing.T, f *unixPacketFixture, request core.Request) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	conn := unixPacketDial(t, f)
	defer conn.Close()
	unixPacketWrite(t, conn, body)
	response := unixPacketRead(t, conn)
	if response.CorrelationID != request.RequestID || response.Error != nil || response.Result == nil {
		t.Fatal("record RPC failed", response)
	}
	return response
}

func unixPacketReceipt(t *testing.T, f *unixPacketFixture, id string) core.Result {
	t.Helper()
	attempt := 0
	return acceptedReceipt(t, 5*time.Second, 40*time.Millisecond, "the record send "+id+" to reach native acceptance", func() *core.Result {
		response := unixPacketCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", id, attempt), Operation: core.ReceiptGet, LookupID: id}).Result.Result
		attempt++
		if response == nil || response.RequestID != id {
			t.Fatal("record receipt mismatch", response)
		}
		return response
	})
}
