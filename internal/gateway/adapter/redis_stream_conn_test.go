package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"reflect"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func streamPeerCommand(t *testing.T, r *bufio.Reader, want ...string) bool {
	t.Helper()
	v, err := rediswire.Read(r)
	var got []string
	for _, field := range v.Array {
		if field.Type != rediswire.Bulk || field.Null {
			t.Error("non-bulk command")
			return false
		}
		got = append(got, string(field.Bytes))
	}
	if err != nil || v.Type != rediswire.Array || !reflect.DeepEqual(got, want) {
		t.Errorf("command %q, want %q: %v", got, want, err)
		return false
	}
	return true
}

func streamConnBroker(b config.RedisPubSub) config.RedisStreams {
	return config.RedisStreams{Address: b.Address, TLSServerName: b.TLSServerName, Credential: b.Subscriber, Group: "gateway", Consumer: "owner", Route: config.RedisStreamRoute{Stream: "events", ResultStream: "results"}}
}

func TestRedisStreamConnCommands(t *testing.T) {
	b, security, count := redisFixture(t, func(_ int, c *tls.Conn, r *bufio.Reader) {
		if !streamPeerCommand(t, r, "AUTH", "reader", "password") {
			return
		}
		_, _ = c.Write([]byte("+OK\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "BLOCK", "1000", "STREAMS", "events", ">") {
			return
		}
		_, _ = c.Write([]byte("*1\r\n*2\r\n$6\r\nevents\r\n*1\r\n*2\r\n$3\r\n1-0\r\n*2\r\n$6\r\nintent\r\n$5\r\ntoken\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "0-0") {
			return
		}
		_, _ = c.Write([]byte("*1\r\n*2\r\n$6\r\nevents\r\n*1\r\n*2\r\n$3\r\n2-0\r\n*-1\r\n"))
		if !streamPeerCommand(t, r, "XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "2-0") {
			return
		}
		_, _ = c.Write([]byte("*-1\r\n"))
		if !streamPeerCommand(t, r, "XADD", "results", "MAXLEN", "=", "1024", "*", "result", "plain") {
			return
		}
		_, _ = c.Write([]byte("$3\r\n3-0\r\n"))
		for _, reply := range []string{":1\r\n", ":0\r\n"} {
			if !streamPeerCommand(t, r, "XACK", "events", "gateway", "1-0") {
				return
			}
			_, _ = c.Write([]byte(reply))
		}
	})
	slots := make(chan struct{}, 1)
	p, err := openRedisStream(t.Context(), streamConnBroker(b), security, []byte("password"), slots)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	entry, err := p.next(">")
	if err != nil || entry == nil || entry.ID != "1-0" || string(entry.Payload) != "token" || entry.Deleted {
		t.Fatalf("entry %+v: %v", entry, err)
	}
	entry, err = p.next("0-0")
	if err != nil || entry == nil || entry.ID != "2-0" || !entry.Deleted {
		t.Fatalf("deleted %+v: %v", entry, err)
	}
	entry, err = p.next("2-0")
	if err != nil || entry != nil {
		t.Fatalf("idle %+v: %v", entry, err)
	}
	if p.publish([]byte("plain")) != nil || p.ack("1-0") != nil || p.ack("1-0") != nil {
		t.Fatal("result or acknowledgement refused")
	}
	p.close()
	p.close()
	if len(slots) != 0 || count.Load() != 1 {
		t.Fatal("socket capacity leak or extra connection")
	}
}

func TestRedisStreamConnInvalidInputs(t *testing.T) {
	b := config.RedisStreams{TLSServerName: "localhost", Credential: config.RedisCredential{Username: "reader"}, Group: "g", Consumer: "c", Route: config.RedisStreamRoute{Stream: "s", ResultStream: "r"}}
	for _, mutate := range []func(*config.RedisStreams, *tls.Config){
		func(b *config.RedisStreams, _ *tls.Config) { b.Group = "" },
		func(b *config.RedisStreams, _ *tls.Config) { b.Consumer = "" },
		func(b *config.RedisStreams, _ *tls.Config) { b.Route.ResultStream = b.Route.Stream },
		func(_ *config.RedisStreams, s *tls.Config) { s.InsecureSkipVerify = true },
		func(_ *config.RedisStreams, s *tls.Config) { s.ServerName = "wrong" },
	} {
		broker, security := b, &tls.Config{ServerName: "localhost"}
		mutate(&broker, security)
		slots := make(chan struct{}, 1)
		if p, err := openRedisStream(context.Background(), broker, security, []byte("password"), slots); err == nil || p != nil || len(slots) != 0 {
			t.Fatal("invalid input acquired socket")
		}
	}
}
