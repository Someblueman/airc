package adapter

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TestSFTPResultNeverTruncated: a result that cannot fit the mailbox even as
// a failure is not written. TestCarrierAdmission covers SFTP admission.
func TestSFTPResultNeverTruncated(t *testing.T) {
	if _, err := encodeSFTPResult(SFTPResult{Version: 1, Protocol: "sftp", RequestID: strings.Repeat("x", MaxSFTPResultBytes)}); err == nil {
		t.Fatal("oversized result encoded")
	}
	small, err := encodeSFTPResult(SFTPResult{Version: 1, Protocol: "sftp", RequestID: "id", Error: &core.Failure{Code: core.Forbidden}})
	if err != nil || len(small) > MaxSFTPResultBytes {
		t.Fatal("result encoding", err)
	}
}

// TestSCPInboxRefusalErrors: the inbox owner sees why an upload was refused.
func TestSCPInboxRefusalErrors(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, config.Listener{ID: "inbox", Protocol: "scp_inbox", Operations: []core.Operation{core.Send}})
	route := config.SCPInbox{Target: core.Target{Kind: "room", Name: "#fixture"}}
	bound := f.author(t, "inboxer", false, false)
	for want, body := range map[error]string{
		auth.ErrForbidden: `{"version":1,"request_id":"scp-guest","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"mail","author":{"nick":"mallory"}}`,
		store.ErrInvalid:  `{"version":1,"request_id":"scp-target","operation":"send","target":{"kind":"room","name":"#other"},"message":"mail"}`,
	} {
		if err := admitSCPInbox(t.Context(), d, route, bound, []byte(body), make(chan struct{}, 1)); !errors.Is(err, want) {
			t.Fatal(err, want)
		}
	}
}

// TestSCPInboxExchange: one request.json upload, acknowledged after intake.
// TestCarrierAdmission covers SCP inbox admission.
func TestSCPInboxExchange(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, config.Listener{ID: "inbox", Protocol: "scp_inbox", Operations: []core.Operation{core.Send}})
	route := config.SCPInbox{Target: core.Target{Kind: "room", Name: "#fixture"}}
	bound := f.author(t, "inboxer", false, false)
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- ServeSCPInbox(t.Context(), server, d, route, bound, make(chan struct{}, 1)) }()
	body := []byte(`{"version":1,"request_id":"scp-wire","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"mail"}`)
	ack := func(step string) {
		var b [1]byte
		if _, err := io.ReadFull(client, b[:]); err != nil || b[0] != 0 {
			t.Fatal(step, b, err)
		}
	}
	ack("ready")
	if _, err := fmt.Fprintf(client, "C0600 %d request.json\n", len(body)); err != nil {
		t.Fatal(err)
	}
	ack("header")
	if _, err := client.Write(append(body, 0)); err != nil {
		t.Fatal(err)
	}
	ack("admitted")
	if err := <-done; err != nil {
		t.Fatal("serve", err)
	}
	if a := f.owner(t, "inboxer", false, "scp-wire"); a != (core.Author{Nick: "inboxer"}) {
		t.Fatal("wire owner", a)
	}
}
