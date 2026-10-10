//go:build linux

package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestUnixPacketListenerJoinsAcceptedConnections(t *testing.T) {
	d, journal, identity, signed, _ := oscoreRPCFixture(t)
	path := unixPacketPath(t)
	listener, err := ListenUnixPacket(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeUnixPacket(ctx, listener, d, make(chan struct{}, 2)) }()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Error("record listener cleanup did not join")
			}
		}
	})
	clients := make([]*net.UnixConn, 0, 2)
	for i := 0; i < 2; i++ {
		conn, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Net: "unixpacket", Name: path})
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, conn)
		t.Cleanup(func() { _ = conn.Close() })
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, _, err := conn.WriteMsgUnix(signed, nil, nil); err != nil || n != len(signed) {
			t.Fatal("send signed control", n, err)
		}
		data := make([]byte, MaxResponseFrameBytes)
		n, _, _, _, err := conn.ReadMsgUnix(data, nil)
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data[:n], &response); err != nil || response.Error == nil || response.Error.Code != core.InvalidRequest {
			t.Fatal("record listener accepted unencrypted signed profile", response, err)
		}
	}
	if _, err := journal.Lookup(t.Context(), identity, "oscore-rpc-message"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unencrypted record reached intake", err)
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("record listener did not join accepted connections")
	}
	for _, conn := range clients {
		var body [1]byte
		if n, _, _, _, err := conn.ReadMsgUnix(body[:], nil); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatal("accepted record connection survived cancellation", n, err)
		}
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal("record listener pathname survived cancellation", err)
	}
}
