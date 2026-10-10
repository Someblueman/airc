//go:build darwin

package adapter

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/testfs"
)

func TestUnixPacketBufferPressureStopsAtWholeWriteDeadline(t *testing.T) {
	root, err := testfs.MkdirTemp("agw-pressure-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	server, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(root, "server"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(root, "client"), Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	packet := make([]byte, 1000)
	if err := client.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	pressure := false
	for i := 0; i < 4096; i++ {
		n, err := client.WriteTo(packet, server.LocalAddr())
		if errors.Is(err, syscall.ENOBUFS) && n == 0 {
			pressure = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !pressure {
		t.Fatal("native socket did not expose definite zero-byte ENOBUFS")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if err := client.SetWriteDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if err := writePacketFragment(ctx, client, packet, server.LocalAddr()); !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("pressure write=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("pressure retry exceeded deadline: %s", elapsed)
	}
}
