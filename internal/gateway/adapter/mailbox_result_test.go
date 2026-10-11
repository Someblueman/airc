package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

// An SFTP result names the upload it answers by SHA-256, and a result that
// cannot fit the mailbox even as a failure is not written. Who an upload
// posts as, and what is refused, runs in TestRouteAdmission.
func TestSFTPResultNamesUploadAndFitsMailbox(t *testing.T) {
	f := newRouteFixture(t)
	b := config.SFTP{ID: "mailbox", Route: config.SFTPRoute{Target: routeTarget, Operations: []core.Operation{core.Send}}}
	body := sendPayload("sftp-bound", "#fixture", "mail", "")
	got := admitSFTP(t.Context(), f.route(t, "mailbox", core.Send), f.author(t, "mailer", true, false), b, body)
	sum := sha256.Sum256(body)
	if got.Error != nil || got.Result == nil || got.RequestID != "sftp-bound" || got.RequestSHA256 != hex.EncodeToString(sum[:]) || got.Target != routeTarget {
		t.Fatalf("result %+v", got)
	}
	if _, err := encodeSFTPResult(SFTPResult{Version: 1, Protocol: "sftp", RequestID: strings.Repeat("x", MaxSFTPResultBytes)}); err == nil {
		t.Fatal("oversized result encoded")
	}
	if small, err := encodeSFTPResult(got); err != nil || len(small) > MaxSFTPResultBytes {
		t.Fatal("result encoding", err)
	}
}

// The SCP exchange is one request.json upload, acknowledged after intake.
func TestSCPInboxAcknowledgesOneUploadAfterIntake(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, config.Listener{ID: "inbox", Protocol: "scp_inbox", Operations: []core.Operation{core.Send}})
	server, client := net.Pipe()
	defer client.Close()
	done := make(chan error, 1)
	go func() {
		done <- ServeSCPInbox(t.Context(), server, d, config.SCPInbox{Target: routeTarget}, f.author(t, "inboxer", false, false), make(chan struct{}, 1))
	}()
	body := sendPayload("scp-wire", "#fixture", "mail", "")
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
