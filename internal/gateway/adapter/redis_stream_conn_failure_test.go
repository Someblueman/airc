package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func TestRedisStreamConnTerminalReplies(t *testing.T) {
	for _, mode := range []string{"auth", "read", "publish", "ack"} {
		t.Run(mode, func(t *testing.T) {
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				if mode == "auth" {
					_, _ = c.Write([]byte("-NOAUTH secret\r\n"))
					return
				}
				_, _ = c.Write([]byte("+OK\r\n"))
				// Read the command, then send a wrong shape for the selected operation.
				if _, err := rediswire.Read(r); err != nil {
					return
				}
				_, _ = c.Write([]byte(":2\r\n"))
			})
			slots := make(chan struct{}, 1)
			p, err := openRedisStream(t.Context(), streamConnBroker(b), security, []byte("password"), slots)
			if mode == "auth" {
				if err != errRedisConnection || p != nil || len(slots) != 0 {
					t.Fatal("authentication failure leaked")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer p.close()
			switch mode {
			case "read":
				_, err = p.next(">")
			case "publish":
				err = p.publish([]byte("signed"))
			case "ack":
				err = p.ack("1-0")
			}
			if err != errRedisConnection || len(slots) != 0 {
				t.Fatal("malformed reply did not release socket", err)
			}
		})
	}
}

func TestRedisStreamConnCancellation(t *testing.T) {
	for _, setup := range []bool{true, false} {
		t.Run(map[bool]string{true: "authentication", false: "blocked_read"}[setup], func(t *testing.T) {
			waiting := make(chan struct{})
			b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
				if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
					return
				}
				if !setup {
					_, _ = c.Write([]byte("+OK\r\n"))
					if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
						return
					}
				}
				close(waiting)
				_, _ = r.ReadByte()
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			slots := make(chan struct{}, 1)
			finished := make(chan error, 1)
			go func() {
				p, err := openRedisStream(ctx, streamConnBroker(b), security, []byte("password"), slots)
				if err == nil {
					_, err = p.next(">")
					p.close()
				}
				finished <- err
			}()
			select {
			case <-waiting:
			case <-time.After(5 * time.Second):
				t.Fatal("fixture did not reach blocked I/O")
			}
			cancel()
			select {
			case err := <-finished:
				if err != errRedisConnection || len(slots) != 0 {
					t.Fatal("cancellation leaked", err)
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not join")
			}
		})
	}
}

func TestRedisStreamConnDeadline(t *testing.T) {
	b, security, _ := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
			return
		}
		_, _ = c.Write([]byte("+OK\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
			return
		}
		_, _ = r.ReadByte()
	})
	slots := make(chan struct{}, 1)
	p, err := openRedisStream(t.Context(), streamConnBroker(b), security, []byte("password"), slots)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	started := time.Now()
	if _, err = p.next(">"); err != errRedisConnection || len(slots) != 0 {
		t.Fatal("deadline leaked socket", err)
	}
	if elapsed := time.Since(started); elapsed < redisDeadline || elapsed > 5*time.Second {
		t.Fatal("production deadline not applied", elapsed)
	}
}
