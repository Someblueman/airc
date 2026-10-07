//go:build darwin || linux

package service

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/testtls"
)

func redisStreamsServicePeer(t *testing.T, raw net.Conn, cert testtls.Certificate, results chan<- []byte, payload []byte) {
	defer raw.Close()
	conn := tls.Server(raw, cert.Server)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	command := func(expected ...string) rediswire.Value {
		v, err := rediswire.Read(r)
		if err != nil || len(v.Array) != len(expected) {
			t.Error("stream command", err)
			return rediswire.Value{}
		}
		for i, word := range expected {
			if word != "" && string(v.Array[i].Bytes) != word {
				t.Errorf("command field %d: %q", i, v.Array[i].Bytes)
				return rediswire.Value{}
			}
		}
		return v
	}
	if len(command("AUTH", "reader", "broker password").Array) == 0 {
		return
	}
	if _, err := conn.Write([]byte("+OK\r\n")); err != nil {
		return
	}
	if len(command("XREADGROUP", "GROUP", "gateway", "owner", "COUNT", "1", "STREAMS", "events", "0-0").Array) == 0 {
		return
	}
	if _, err := fmt.Fprintf(conn, "*1\r\n*2\r\n$6\r\nevents\r\n*1\r\n*2\r\n$3\r\n1-0\r\n*2\r\n$6\r\nintent\r\n$%d\r\n%s\r\n", len(payload), payload); err != nil {
		return
	}
	result := command("XADD", "results", "MAXLEN", "=", "1024", "*", "result", "")
	if len(result.Array) == 0 {
		return
	}
	if _, err := conn.Write([]byte("$3\r\n2-0\r\n")); err != nil {
		return
	}
	if len(command("XACK", "events", "gateway", "1-0").Array) == 0 {
		return
	}
	if _, err := conn.Write([]byte(":1\r\n")); err != nil {
		return
	}
	results <- result.Array[7].Bytes
	_, _ = r.ReadByte()
}
