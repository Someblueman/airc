//go:build darwin || linux

package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/testtls"
	"net"
	"testing"
	"time"
)

func redisServicePeer(t *testing.T, ctx context.Context, i int, raw net.Conn, cert testtls.Certificate, results chan<- []byte, payload []byte) {

	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = raw.Close(); close(closed) })
	defer func() {
		if !stop() {
			<-closed
		}
	}()
	defer raw.Close()
	conn := tls.Server(raw, cert.Server)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	r := bufio.NewReader(conn)
	v, err := rediswire.Read(r)
	user := "writer"
	if i == 1 {
		user = "reader"
	}
	if err != nil || len(v.Array) != 3 || string(v.Array[0].Bytes) != "AUTH" || string(v.Array[1].Bytes) != user || string(v.Array[2].Bytes) != "broker password" {
		t.Error("AUTH", err)
		return
	}
	write := func(v rediswire.Value) bool {
		wire, err := rediswire.Encode(v)
		if err != nil {
			t.Error(err)
			return false
		}
		_, err = conn.Write(wire)
		return err == nil
	}
	if !write(rediswire.Value{Type: rediswire.Simple, Bytes: []byte("OK")}) {
		return
	}
	v, err = rediswire.Read(r)
	if err != nil {
		return
	}
	if i == 0 {
		if len(v.Array) != 3 || string(v.Array[0].Bytes) != "PUBLISH" || string(v.Array[1].Bytes) != "response" {
			t.Error("PUBLISH")
			return
		}
		results <- v.Array[2].Bytes
		if !write(rediswire.Value{Type: rediswire.Integer, Integer: 1}) {
			return
		}
	} else {
		if len(v.Array) != 2 || string(v.Array[0].Bytes) != "SUBSCRIBE" || string(v.Array[1].Bytes) != "request" {
			t.Error("SUBSCRIBE")
			return
		}
		if !write(rediswire.Value{Type: rediswire.Array, Array: []rediswire.Value{{Type: rediswire.Bulk, Bytes: []byte("subscribe")}, {Type: rediswire.Bulk, Bytes: []byte("request")}, {Type: rediswire.Integer, Integer: 1}}}) {
			return
		}
		if !write(rediswire.Value{Type: rediswire.Array, Array: []rediswire.Value{{Type: rediswire.Bulk, Bytes: []byte("message")}, {Type: rediswire.Bulk, Bytes: []byte("request")}, {Type: rediswire.Bulk, Bytes: payload}}}) {
			return
		}
	}
	_, _ = r.ReadByte()
}
