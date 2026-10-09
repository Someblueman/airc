//go:build darwin || linux

package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

// The pipe exercises application admission and cancellation under the host race
// detector. Native Linux socket and gateway tests separately prove SCTP records.
func sctpApplicationFixture(t *testing.T) (amqpAdmissionFixture, FrameRequest, *ConnectionResponseProtection, func([]byte) FrameResponse) {
	t.Helper()
	f, encrypt := localREPReadFixture(t)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	protection, err := NewConnectionResponseProtection(sealer, f.c.Realm, []ConnectionReturnKey{{ID: "route", Principal: f.identity.ID(), Binding: f.identity.Binding(), RecipientID: "reader", Recipient: &recipient.PublicKey}})
	if err != nil {
		t.Fatal(err)
	}
	envelope := localREPEncryptedRequest(t, f, encrypt, amqpRequest())
	digest := sha256.Sum256([]byte(envelope))
	request := FrameRequest{Version: 1, CorrelationID: hex.EncodeToString(digest[:]), Profile: "encrypted", Envelope: envelope}
	open := func(token []byte) FrameResponse {
		t.Helper()
		scope := auth.ResponseScope{Realm: f.c.Realm, Principal: f.identity.ID(), Binding: f.identity.Binding(), Route: "route"}
		plain, err := auth.OpenResponse(string(token), "response", pub, "reader", recipient, scope, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var response FrameResponse
		if err := json.Unmarshal(plain, &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	return f, request, protection, open
}

func startSCTPApplication(t *testing.T, f amqpAdmissionFixture, request FrameRequest, protection *ConnectionResponseProtection, slots chan struct{}, writing chan struct{}, mode SCTPMode) (net.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	server, client := net.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	var conn net.Conn = server
	if writing != nil {
		conn = &sctpObservedWriter{Conn: server, writing: writing}
	}
	go func() {
		defer close(finished)
		done <- serveSCTPConnection(ctx, conn, f.d, slots, protection, mode)
	}()
	t.Cleanup(func() {
		cancel()
		_ = client.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Error("SCTP test connection did not join")
		}
	})
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(body); err != nil {
		t.Fatal(err)
	}
	return client, cancel, done
}

type sctpObservedWriter struct {
	net.Conn
	writing chan struct{}
}

func (c *sctpObservedWriter) Write(body []byte) (int, error) {
	close(c.writing)
	return c.Conn.Write(body)
}
