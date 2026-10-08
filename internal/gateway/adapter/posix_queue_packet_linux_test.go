//go:build linux && (arm64 || amd64)

package adapter

import (
	"bytes"
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func queuePacketFixture(t *testing.T) (*queuePackets, *POSIXQueue, *POSIXQueue) {
	t.Helper()
	ingress, response := newTestPOSIXQueue(t, 1), newTestPOSIXQueue(t, 1)
	producer, err := OpenPOSIXQueue(ingress.Name(), 1)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := OpenPOSIXQueue(response.Name(), 1)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := NewPOSIXQueuePackets(ingress, map[string]*POSIXQueue{response.Name(): writer})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(); _ = producer.Close() })
	return conn, producer, response
}

func TestPOSIXQueuePacketsWholeAndFixedRoutes(t *testing.T) {
	conn, producer, response := queuePacketFixture(t)
	body := bytes.Repeat([]byte("p"), POSIXQueueMessageBytes)
	if err := producer.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadFrom(make([]byte, POSIXQueueMessageBytes-1)); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal(err)
	}
	buffer := make([]byte, POSIXQueueMessageBytes+1)
	n, source, err := conn.ReadFrom(buffer)
	if err != nil || n != len(body) || !bytes.Equal(buffer[:n], body) || source.String() != conn.LocalAddr().String() || source.Network() != "posix_mqueue" {
		t.Fatalf("read %d %v %v", n, source, err)
	}
	route := &POSIXQueueAddr{Name: response.Name()}
	if !validPacketDestination(route) || clonePacketAddr(route) == route {
		t.Fatal("route validation or clone")
	}
	if _, err := conn.WriteTo(body, &POSIXQueueAddr{Name: "/unknown"}); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo(body, &net.UnixAddr{Name: response.Name(), Net: "unixgram"}); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal(err)
	}
	if _, err := conn.WriteTo(append(body, 1), route); !errors.Is(err, ErrPOSIXQueueInvalid) {
		t.Fatal(err)
	}
	n, err = conn.WriteTo(body, route)
	if err != nil || n != len(body) {
		t.Fatalf("write %d %v", n, err)
	}
	n, _, err = response.ReadMessage(t.Context(), buffer)
	if err != nil || !bytes.Equal(buffer[:n], body) {
		t.Fatal("response", err)
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := OpenPOSIXQueue(response.Name(), 1)
	if err != nil {
		t.Fatal("external response name removed", err)
	}
	_ = retained.Close()
	if _, err = OpenPOSIXQueue(conn.LocalAddr().String(), 1); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ingress remains", err)
	}
}

func TestPOSIXQueuePacketsMutableDeadlinesAndClose(t *testing.T) {
	conn, _, response := queuePacketFixture(t)
	read := make(chan error, 1)
	go func() { _, _, err := conn.ReadFrom(make([]byte, POSIXQueueMessageBytes)); read <- err }()
	time.Sleep(40 * time.Millisecond)
	if err := conn.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-read:
		var ne net.Error
		if !errors.Is(err, os.ErrDeadlineExceeded) || !errors.As(err, &ne) || !ne.Timeout() {
			t.Fatal(err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("deadline did not wake read")
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		t.Fatal(err)
	}
	if err := response.WriteMessage(t.Context(), []byte("full")); err != nil {
		t.Fatal(err)
	}
	write := make(chan error, 1)
	go func() {
		_, err := conn.WriteTo([]byte("blocked"), &POSIXQueueAddr{Name: response.Name()})
		write <- err
	}()
	go func() { _, _, err := conn.ReadFrom(make([]byte, POSIXQueueMessageBytes)); read <- err }()
	time.Sleep(40 * time.Millisecond)
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{read, write} {
		select {
		case err := <-done:
			if !errors.Is(err, net.ErrClosed) {
				t.Fatal(err)
			}
		case <-time.After(300 * time.Millisecond):
			t.Fatal("close failed to join")
		}
	}
	if err := conn.SetDeadline(time.Time{}); !errors.Is(err, net.ErrClosed) {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}
