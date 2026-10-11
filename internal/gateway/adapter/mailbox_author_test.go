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

func mailboxSend(id, room, author string) []byte {
	body := `{"version":1,"request_id":"` + id + `","operation":"send","target":{"kind":"room","name":"` + room + `"},"message":"mail"`
	if author != "" {
		body += `,"author":` + author
	}
	return []byte(body + "}")
}

func TestSFTPMailboxPostsAsBodyOrBoundAuthor(t *testing.T) {
	f := newRouteFixture(t)
	d := f.route(t, "mailbox", core.Send)
	b := config.SFTP{ID: "mailbox", Route: config.SFTPRoute{Target: core.Target{Kind: "room", Name: "#fixture"}, Operations: []core.Operation{core.Send}}}
	bound := f.author(t, "mailer", true, false)

	got := admitSFTP(t.Context(), d, bound, b, mailboxSend("sftp-bound", "#fixture", ""))
	if got.Error != nil || got.Result == nil || got.Result.Result == nil || !got.Result.Result.Durable || got.RequestID != "sftp-bound" || len(got.RequestSHA256) != 64 {
		t.Fatalf("bound send %+v", got)
	}
	if a := f.owner(t, "mailer", true, "sftp-bound"); a != (core.Author{Nick: "mailer", Registered: true}) {
		t.Fatal("bound owner", a)
	}
	// A guest on a bound mailbox is refused; another target or operation is
	// forbidden before dispatch; an undecodable upload is invalid.
	refusals := map[string][]byte{
		"guest":     mailboxSend("sftp-guest", "#fixture", `{"nick":"mallory"}`),
		"target":    mailboxSend("sftp-target", "#other", ""),
		"operation": []byte(`{"version":1,"request_id":"sftp-read","operation":"history.read","target":{"kind":"room","name":"#fixture"}}`),
	}
	for name, body := range refusals {
		if got := admitSFTP(t.Context(), d, bound, b, body); got.Error == nil || got.Result != nil {
			t.Fatal(name, got)
		}
	}
	if got := admitSFTP(t.Context(), d, bound, b, []byte("{")); got.Error == nil || got.Error.Code != core.InvalidRequest || got.RequestID != "" {
		t.Fatal("undecodable", got)
	}
	for _, id := range []string{"sftp-guest", "sftp-target", "sftp-read"} {
		if f.jobs(t, id) != 0 {
			t.Fatal("refused intake", id)
		}
	}
	// A guest mailbox posts the named guest.
	guests := f.author(t, "", false, true)
	if got := admitSFTP(t.Context(), d, guests, b, mailboxSend("sftp-named", "#fixture", `{"nick":"box-guest"}`)); got.Error != nil {
		t.Fatal("guest send", got.Error)
	}
	if a := f.owner(t, "box-guest", false, "sftp-named"); a != (core.Author{Nick: "box-guest"}) {
		t.Fatal("guest owner", a)
	}
	// A result that cannot fit the mailbox even as a failure is not written.
	if _, err := encodeSFTPResult(SFTPResult{Version: 1, Protocol: "sftp", RequestID: strings.Repeat("x", MaxSFTPResultBytes)}); err == nil {
		t.Fatal("oversized result encoded")
	}
	small, err := encodeSFTPResult(got)
	if err != nil || len(small) > MaxSFTPResultBytes {
		t.Fatal("result encoding", err)
	}
}

func TestSCPInboxPostsAsBodyOrBoundAuthor(t *testing.T) {
	f := newRouteFixture(t)
	l := config.Listener{ID: "inbox", Protocol: "scp_inbox", Operations: []core.Operation{core.Send}}
	d := f.listener(t, l)
	route := config.SCPInbox{Target: core.Target{Kind: "room", Name: "#fixture"}}
	requests := make(chan struct{}, 1)
	bound := f.author(t, "inboxer", false, false)

	if err := admitSCPInbox(t.Context(), d, route, bound, mailboxSend("scp-bound", "#fixture", ""), requests); err != nil {
		t.Fatal("bound upload", err)
	}
	if a := f.owner(t, "inboxer", false, "scp-bound"); a != (core.Author{Nick: "inboxer"}) {
		t.Fatal("bound owner", a)
	}
	if err := admitSCPInbox(t.Context(), d, route, bound, mailboxSend("scp-reg", "#fixture", `{"nick":"carol","token":"`+authorTestToken+`"}`), requests); err != nil {
		t.Fatal("registered upload", err)
	}
	if a := f.owner(t, "carol", true, "scp-reg"); a != (core.Author{Nick: "carol", Registered: true}) {
		t.Fatal("registered owner", a)
	}
	if err := admitSCPInbox(t.Context(), d, route, bound, mailboxSend("scp-guest", "#fixture", `{"nick":"mallory"}`), requests); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("guest on bound inbox", err)
	}
	if err := admitSCPInbox(t.Context(), d, route, bound, mailboxSend("scp-target", "#other", ""), requests); !errors.Is(err, store.ErrInvalid) {
		t.Fatal("other target", err)
	}
	if f.jobs(t, "scp-guest")+f.jobs(t, "scp-target") != 0 {
		t.Fatal("refused intake")
	}
	// The SCP exchange: one request.json upload, acknowledged after intake.
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() { done <- ServeSCPInbox(t.Context(), server, d, route, bound, requests) }()
	body := mailboxSend("scp-wire", "#fixture", "")
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
