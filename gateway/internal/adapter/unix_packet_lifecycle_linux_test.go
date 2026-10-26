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
)

func TestUnixPacketListenerJoinsAcceptedConnections(t *testing.T) {
	f, plain := frameFixture(t)
	path := unixPacketPath(t)
	listener, err := ListenUnixPacket(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- ServeUnixPacket(ctx, listener, f.d, make(chan struct{}, 2)) }()
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
		if n, _, err := conn.WriteMsgUnix(plain, nil, nil); err != nil || n != len(plain) {
			t.Fatal("send", n, err)
		}
		data := make([]byte, MaxResponseFrameBytes)
		n, _, _, _, err := conn.ReadMsgUnix(data, nil)
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(data[:n], &response); err != nil || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal("record listener refused a plain request", response, err)
		}
	}
	if f.jobs(t, "frame-message") != 1 {
		t.Fatal("replayed record was not one durable job")
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
