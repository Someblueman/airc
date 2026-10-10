//go:build (darwin || linux) && arm64

package adapter

import (
	"bytes"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"golang.org/x/sys/unix"
	"net"
	"testing"
)

func TestSystemVQueuePacketsWholeAndFixedRoutes(t *testing.T) {
	conn, producer, response := systemVPacketsFixture(t)
	body := bytes.Repeat([]byte("p"), sysvqueue.MessageBytes)
	if err := producer.WriteMessage(t.Context(), body); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadFrom(make([]byte, 999)); !errors.Is(err, sysvqueue.ErrInvalid) {
		t.Fatal(err)
	}
	buffer := make([]byte, 1001)
	n, source, err := conn.ReadFrom(buffer)
	if err != nil || n != 1000 || !bytes.Equal(buffer[:n], body) || source.String() != conn.LocalAddr().String() || source.Network() != "system_v_queue" {
		t.Fatal(n, source, err)
	}
	source.(*SystemVQueueAddr).Key = 0
	if conn.LocalAddr().(*SystemVQueueAddr).Key <= 0 {
		t.Fatal("mutable local address")
	}
	route := &SystemVQueueAddr{Key: response.Identity().Key}
	if !validPacketDestination(route) || clonePacketAddr(route) == route || packetWatchMode(conn) != "system_v_queue_mailbox_fragments" {
		t.Fatal("route validation/clone/watch")
	}
	for _, addr := range []net.Addr{&SystemVQueueAddr{Key: 0}, &SystemVQueueAddr{Key: producer.Identity().Key}, &POSIXQueueAddr{Name: "/other"}, (*SystemVQueueAddr)(nil)} {
		if _, err := conn.WriteTo(body, addr); !errors.Is(err, sysvqueue.ErrInvalid) {
			t.Fatal("unknown destination", err)
		}
	}
	if _, err := conn.WriteTo(append(body, 1), route); !errors.Is(err, sysvqueue.ErrInvalid) {
		t.Fatal(err)
	}
	if n, err := conn.WriteTo(body, route); err != nil || n != 1000 {
		t.Fatal(n, err)
	}
	n, err = response.ReadMessage(t.Context(), buffer)
	if err != nil || !bytes.Equal(buffer[:n], body) {
		t.Fatal(n, err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := sysvqueue.Open(response.Identity().Key)
	if err != nil {
		t.Fatal("response removed", err)
	}
	_ = retained.Close()
	if _, err := sysvqueue.Open(producer.Identity().Key); !errors.Is(err, unix.ENOENT) {
		t.Fatal("ingress remains", err)
	}
}
func TestSystemVQueuePacketsConstructorRefusal(t *testing.T) {
	q := systemVQueueFixture(t)
	for _, routes := range []map[int32]*sysvqueue.Queue{nil, {q.Identity().Key: q}, {1: nil}, {0: q}, {1: q}} {
		if _, err := NewSystemVQueuePackets(q, routes); !errors.Is(err, sysvqueue.ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := NewSystemVQueuePackets(nil, map[int32]*sysvqueue.Queue{q.Identity().Key: q}); !errors.Is(err, sysvqueue.ErrInvalid) {
		t.Fatal(err)
	}
	if err := q.WriteMessage(t.Context(), []byte("retained")); err != nil {
		t.Fatal("failed constructor took ownership", err)
	}
}
