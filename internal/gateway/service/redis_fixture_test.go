//go:build darwin || linux

package service

import (
	"bufio"
	"context"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/internal/testtls"
)

// redisServiceConfig has no signing material at all: the route principal has
// no credential of its own and no response signer is configured.
func redisServiceConfig(t *testing.T) (config.Config, testtls.Certificate) {
	t.Helper()
	c := lifecycleConfig(t)
	root, err := filepath.EvalSymlinks(filepath.Dir(c.StateDir))
	if err != nil {
		t.Fatal(err)
	}
	account := filepath.Join(root, "account")
	if err := auth.CreateCredential(account); err != nil {
		t.Fatal(err)
	}
	c.Bindings = []config.Binding{{ID: "backend", Network: "unix", Address: filepath.Join(root, "native.sock"), Account: "author", AccountTokenFile: account}}
	c.Principals = nil
	c.Limits.Sessions, c.Limits.SessionsPerPrincipal, c.Limits.Workers = 2, 1, 1
	c.Limits.Watches, c.Limits.WatchesPerPrincipal = 0, 0
	return c, testtls.New(t)
}

func redisServicePassword(t *testing.T, c config.Config, name string) string {
	t.Helper()
	path := filepath.Join(filepath.Dir(c.StateDir), name)
	if err := os.WriteFile(path, []byte("broker password"), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func redisServicePubSub(t *testing.T, c config.Config, cert testtls.Certificate, address string) config.RedisPubSub {
	t.Helper()
	return config.RedisPubSub{ID: "redis", Enabled: true, Address: address, TLSServerName: "localhost", TLSCAFile: cert.CAPath,
		Subscriber: config.RedisCredential{Username: "reader", PasswordFile: redisServicePassword(t, c, "reader-password")}, Publisher: config.RedisCredential{Username: "writer", PasswordFile: redisServicePassword(t, c, "writer-password")},
		Routes: []config.RedisRoute{{RequestChannel: "request", ResponseChannel: "response", Target: redisServiceTarget, Operations: []core.Operation{core.Send}}}}
}

func redisServiceStreams(t *testing.T, c config.Config, cert testtls.Certificate, address string) config.RedisStreams {
	t.Helper()
	return config.RedisStreams{ID: "streams", Enabled: true, Namespace: "broker", Address: address, TLSServerName: "localhost", TLSCAFile: cert.CAPath,
		Credential: config.RedisCredential{Username: "reader", PasswordFile: redisServicePassword(t, c, "reader-password")}, Group: "gateway", Consumer: "owner",
		Route: config.RedisStreamRoute{Stream: "events", ResultStream: "results", Target: redisServiceTarget, Operations: []core.Operation{core.Send}}}
}

var redisServiceTarget = core.Target{Kind: "room", Name: "#fixture"}

// redisServiceRequest is the plain contract-v1 body an HTTP client would send,
// naming a guest author.
func redisServiceRequest(c config.Config) (core.Request, []byte) {
	target := redisServiceTarget
	request := core.Request{Version: 1, RequestID: "service-send", Operation: core.Send, Target: &target, Message: "service body 世界\nsecond line"}
	return request, []byte(`{"version":1,"request_id":"service-send","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"service body 世界\nsecond line","author":{"nick":"redis-guest"}}`)
}

// redisServiceJournal reopens the journal after shutdown and returns the
// guest author's job.
func redisServiceJournal(t *testing.T, c config.Config, requestID string) store.Job {
	t.Helper()
	journal, err := store.Open(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	job, err := journal.LookupAuthor(t.Context(), store.Submission{Author: core.Author{Nick: "redis-guest"}}, requestID)
	if err != nil {
		t.Fatal("journal", err)
	}
	return job
}

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
