//go:build darwin || linux

package adapter

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/rediswire"
)

func streamPeerEntry(c *tls.Conn, stream string, entry rediswire.StreamEntry) error {
	var wire bytes.Buffer
	fmt.Fprintf(&wire, "*1\r\n*2\r\n$%d\r\n%s\r\n*1\r\n*2\r\n$%d\r\n%s\r\n", len(stream), stream, len(entry.ID), entry.ID)
	if entry.Deleted {
		wire.WriteString("*-1\r\n")
	} else {
		fmt.Fprintf(&wire, "*2\r\n$6\r\nintent\r\n$%d\r\n", len(entry.Payload))
		wire.Write(entry.Payload)
		wire.WriteString("\r\n")
	}
	_, err := c.Write(wire.Bytes())
	return err
}

func streamPeerResult(t *testing.T, c *tls.Conn, r *bufio.Reader, b config.RedisStreams) []byte {
	t.Helper()
	v, err := rediswire.Read(r)
	if err != nil || v.Type != rediswire.Array || len(v.Array) != 8 {
		t.Error("missing result command", err)
		return nil
	}
	expected := []string{"XADD", b.Route.ResultStream, "MAXLEN", "=", "1024", "*", "result"}
	for i, want := range expected {
		if v.Array[i].Type != rediswire.Bulk || string(v.Array[i].Bytes) != want {
			t.Error("wrong result command")
			return nil
		}
	}
	if v.Array[7].Type != rediswire.Bulk || v.Array[7].Null {
		t.Error("wrong result payload")
		return nil
	}
	return v.Array[7].Bytes
}

func streamOpenResult(t *testing.T, f amqpAdmissionFixture, b config.RedisStreams, pub ed25519.PublicKey, entryID string, token []byte) RedisStreamResult {
	t.Helper()
	scope := redisStreamResponseScope(f.c.Realm, b.Route.Principal, f.identity.Binding(), b, entryID)
	plain, err := auth.OpenSignedResponse(string(token), "response", pub, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var result RedisStreamResult
	if err = json.Unmarshal(plain, &result); err != nil {
		t.Fatal(err)
	}
	if result.EntryID != entryID || result.Namespace != b.Namespace || result.Target != b.Route.Target {
		t.Fatal("wrong result correlation")
	}
	return result
}
