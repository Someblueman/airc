//go:build darwin || linux

package gateway_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestStdioPipeExecutesFramesAndEOFBoundaries(t *testing.T) {
	socket := fixture(t)
	observer := dial(t, socket, "stdio-observer", "", false, false)
	defer observer.Close()
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "stdio-observer" })
	provision := dial(t, socket, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, socket, fixtureAccount)
	credentials := provisionDetached(t, &c)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "stdio", Protocol: "stdio", Enabled: true, Operations: dispatch.ImplementedOperations()}}
	binaryPath := gatewayBinary(t)
	// EOF immediately after one complete frame must still drain the correlated
	// result before the service closes its store.
	capEOF := startStdio(t, binaryPath, c)
	capRequest := core.Request{Version: 1, RequestID: "stdio-capabilities", Operation: core.CapabilitiesGet}
	capEOF.write(t, frameRequest{Version: 1, CorrelationID: "cap-eof", Profile: "plain", Request: credentials.token(t, capRequest, false)})
	if err := capEOF.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	capResponse := capEOF.read(t)
	if err := capEOF.wait(); err != nil {
		t.Fatalf("signed capabilities EOF did not join: %v stderr=%s", err, capEOF.stderr.String())
	}
	if capResponse.Version != 1 || capResponse.CorrelationID != "cap-eof" || capResponse.Event != "result" || capResponse.Result == nil || capResponse.Result.Capabilities == nil {
		t.Fatalf("signed capabilities EOF failed: %+v", capResponse)
	}
	expectedOps := dispatch.ImplementedOperations()
	if !slices.Equal(capResponse.Result.Capabilities.Operations, expectedOps) {
		t.Fatalf("stdio capabilities operations=%v want=%v", capResponse.Result.Capabilities.Operations, expectedOps)
	}

	// Authentication errors remain typed and correlated on the same pipe.
	p := startStdio(t, binaryPath, c)
	p.write(t, frameRequest{Version: 1, CorrelationID: "cap-signed", Profile: "plain", Request: credentials.token(t, capRequest, false)})
	if response := p.read(t); response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("signed capabilities failed: %+v", response)
	}
	p.write(t, frameRequest{Version: 1, CorrelationID: "cap-encrypted", Profile: "plain", Request: credentials.token(t, capRequest, true)})
	if response := p.read(t); response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("encrypted capabilities failed: %+v", response)
	}
	p.write(t, frameRequest{Version: 1, CorrelationID: "tampered", Profile: "plain", Request: "{}"})
	if response := p.read(t); response.Version != 1 || response.CorrelationID != "tampered" || response.Event != "error" || response.Error == nil || response.Error.Code != core.InvalidRequest {
		t.Fatalf("tampered frame was accepted: %+v", response)
	}
	if err := p.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.wait(); err != nil {
		t.Fatalf("stdio auth process did not join: %v stderr=%s", err, p.stderr.String())
	}

	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	send := core.Request{Version: 1, RequestID: "stdio-send", Operation: core.Send, Target: target, Message: "stdio whole body\n🙂"}
	mutation := startStdio(t, binaryPath, c)
	mutation.write(t, frameRequest{Version: 1, CorrelationID: "send-eof", Profile: "plain", Request: credentials.token(t, send, true)})
	if err := mutation.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	admission := mutation.read(t)
	if admission.Event != "result" || admission.Result == nil || admission.Result.Result == nil || !admission.Result.Result.Durable {
		t.Fatalf("stdio send was not admitted durably: %+v", admission)
	}
	if admission.CorrelationID != "send-eof" || admission.Version != 1 {
		t.Fatalf("stdio send response identity lost: %+v", admission)
	}
	if err := mutation.wait(); err != nil {
		t.Fatalf("stdio mutation EOF did not join: %v stderr=%s", err, mutation.stderr.String())
	}
	accepted := next(t, observer, func(e *irc.MessageEvent) bool { return e.Message == send.Message && e.Target == target.Name })
	if accepted.Message != send.Message || accepted.Target != target.Name || accepted.From != "fixture-author" || accepted.AccountID == "" {
		t.Fatalf("native observer lost stdio message facts: %+v", accepted)
	}
	receipt := startStdio(t, binaryPath, c)
	receiptRequest := core.Request{Version: 1, RequestID: "stdio-receipt", Operation: core.ReceiptGet, LookupID: send.RequestID}
	receipt.write(t, frameRequest{Version: 1, CorrelationID: "receipt-eof", Profile: "plain", Request: credentials.token(t, receiptRequest, true)})
	if err := receipt.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	receiptResponse := receipt.read(t)
	if err := receipt.wait(); err != nil {
		t.Fatalf("receipt EOF did not join: %v stderr=%s", err, receipt.stderr.String())
	}
	if receiptResponse.Event != "result" || receiptResponse.Result == nil || receiptResponse.Result.Result == nil || receiptResponse.Result.Result.State != core.Accepted || receiptResponse.Result.Result.SendAttempts != 1 || receiptResponse.Result.Result.Persisted == nil || !*receiptResponse.Result.Result.Persisted {
		t.Fatalf("reopened stdio receipt was not accepted/persisted once: %+v", receiptResponse)
	}

}
