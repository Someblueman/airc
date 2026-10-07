package adapter

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

type kafkaPartialConn struct {
	net.Conn
	first  []byte
	writes int
}

func (c *kafkaPartialConn) Write(b []byte) (int, error) {
	if c.first == nil {
		c.first = b
	}
	c.writes++
	return c.Conn.Write(b[:min(3, len(b))])
}

func TestKafkaCommandPartialWrites(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	done := make(chan []byte, 1)
	go func() {
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		var header [4]byte
		if _, e := io.ReadFull(server, header[:]); e != nil {
			done <- nil
			return
		}
		b := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, e := io.ReadFull(server, b); e != nil {
			done <- nil
			return
		}
		_, _ = server.Write([]byte{0, 0, 0, 6, 0, 0, 0, 7, 'o', 'k'})
		done <- b
	}()
	c := &kafkaPartialConn{Conn: client}
	body := []byte("password")
	reply, err := kafkaCommand(c, 36, 1, 7, body, time.Now().Add(time.Second))
	wire := <-done
	if err != nil || string(reply) != "ok" || c.writes < 2 || len(wire) < 22 || !bytes.Equal(wire[22:], body) {
		t.Fatalf("exchange %x %v", reply, err)
	}
	if !bytes.Equal(c.first, make([]byte, len(c.first))) || string(body) != "password" {
		t.Fatal("credential clearing or input ownership")
	}
}

func TestKafkaCommandReplyRefusal(t *testing.T) {
	for _, reply := range [][]byte{{0, 0, 0, 4, 0, 0, 0, 8}, {255, 255, 255, 255}, {0, 0, 0, 8, 0, 0, 0, 7, 'x'}} {
		client, server := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(time.Second))
			var header [4]byte
			if _, e := io.ReadFull(server, header[:]); e != nil {
				return
			}
			b := make([]byte, binary.BigEndian.Uint32(header[:]))
			if _, e := io.ReadFull(server, b); e != nil {
				return
			}
			_, _ = server.Write(reply)
		}()
		if b, e := kafkaCommand(client, 18, 0, 7, nil, time.Now().Add(time.Second)); e != errKafkaConnection || b != nil {
			t.Fatal("invalid reply accepted")
		}
		_ = client.Close()
		<-done
	}
}

func TestKafkaCommandDeadline(t *testing.T) {
	// An unread peer blocks the write, which must share the complete exchange deadline.
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	start := time.Now()
	if b, e := kafkaCommand(client, 18, 0, 0, nil, start.Add(25*time.Millisecond)); e != errKafkaConnection || b != nil {
		t.Fatal("blocked write accepted")
	}
	if time.Since(start) > time.Second {
		t.Fatal("write ignored absolute deadline")
	}
	if _, e := kafkaCommand(client, 18, 0, 0, nil, time.Now().Add(-time.Second)); e != errKafkaConnection {
		t.Fatal("expired deadline")
	}
	if _, e := kafkaCommand(nil, 18, 0, 0, nil, time.Time{}); e != errKafkaConnection {
		t.Fatal("nil connection")
	}
	if _, e := kafkaCommand(client, -1, 0, 0, nil, time.Time{}); e != errKafkaConnection {
		t.Fatal("invalid command")
	}
}

func TestKafkaCommandReadDeadline(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		_ = server.SetDeadline(time.Now().Add(time.Second))
		var header [4]byte
		if _, e := io.ReadFull(server, header[:]); e != nil {
			return
		}
		b := make([]byte, binary.BigEndian.Uint32(header[:]))
		if _, e := io.ReadFull(server, b); e != nil {
			return
		}
		// Complete the reply header but leave its promised body unavailable.
		_, _ = server.Write([]byte{0, 0, 0, 8, 0, 0, 0, 7})
		_, _ = server.Read(header[:1])
	}()
	start := time.Now()
	b, e := kafkaCommand(client, 18, 0, 7, nil, start.Add(25*time.Millisecond))
	_ = client.Close()
	<-done
	if e != errKafkaConnection || b != nil || time.Since(start) > time.Second {
		t.Fatal("reply body ignored exchange deadline", e)
	}
}
