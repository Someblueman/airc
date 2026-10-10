//go:build linux && arm64

package gateway_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"golang.org/x/sys/unix"
)

func TestSCTPPRTTLRecordExpiresBeforeGatewayIntakeAndCanBeRetried(t *testing.T) {
	f := newSCTPFixtureMode(t, "ipv4", adapter.SCTPOrderedTTL, true)
	if response := sctpCall(t, f, f.client, core.Request{Version: 1, RequestID: "sctp-pr-ttl-preflight", Operation: core.CapabilitiesGet}); response.Event != "result" || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("PR-TTL preflight capabilities=%+v", response)
	}
	requestID := "sctp-pr-ttl-expired-before-intake"
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: requestID, Operation: core.Send, Target: target, Message: "valid SCTP PR-TTL retry after kernel expiry 世界", IssuedAt: &issued, ExpiresAt: &expiry}
	frame := sctpFrame(t, f, request, true)
	targetWire := sctpPadRequestToLimit(t, sctpRequestBytes(t, frame))
	assoc, _, err := sctpTestAssociationStatus(f.client.fd)
	if err != nil {
		t.Fatalf("read SCTP association before expiry test: %v", err)
	}
	baseUnsent, baseSent, err := sctpTestPRAssocStatus(f.client.fd, assoc)
	if err != nil {
		t.Fatalf("read PR-SCTP baseline: %v", err)
	}

	if err := f.process.command.Process.Signal(syscall.SIGSTOP); err != nil {
		t.Fatalf("stop gateway before native intake: %v", err)
	}
	stopped := true
	defer func() {
		if stopped {
			if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
				_ = f.process.command.Process.Kill()
			}
		}
	}()
	waitSCTPGatewayStopped(t, f.process.command.Process.Pid)

	const (
		maxFillerRecords = 128
		fillerBytes      = 4 << 10 // Leave a positive window smaller than the TTL record.
		windowTarget     = 8 << 10
	)
	fillerCorrelations := make(map[string]bool, maxFillerRecords)
	peerWindow := uint32(0)
	for i := 0; i < maxFillerRecords; i++ {
		_, before, statusErr := sctpTestAssociationStatus(f.client.fd)
		if statusErr != nil {
			t.Fatalf("query peer window before reliable filler: %v", statusErr)
		}
		if before == 0 {
			t.Fatal("reliable fillers exhausted the peer window before a PR-TTL record could begin transmission")
		}
		if before <= windowTarget {
			peerWindow = before
			break
		}
		filler := core.Request{Version: 1, RequestID: fmt.Sprintf("sctp-pr-fill-%03d", i), Operation: core.CapabilitiesGet}
		fillerFrame := sctpFrame(t, f, filler, true)
		body := sctpPadRequestToSize(t, sctpRequestBytes(t, fillerFrame), fillerBytes)
		sent, sendErr := f.client.trySendRecord(body, adapter.SCTPOrdered, false)
		if errors.Is(sendErr, unix.EAGAIN) || errors.Is(sendErr, unix.EWOULDBLOCK) {
			break
		}
		if sendErr != nil {
			t.Fatalf("fill stopped gateway SCTP receive window: %v", sendErr)
		}
		if !sent {
			break
		}
		fillerCorrelations[fillerFrame.CorrelationID] = true
		peerWindow, err = waitSCTPPeerWindowDrop(f.client.fd, before, time.Now().Add(500*time.Millisecond))
		if err != nil {
			t.Fatalf("observe reliable filler entering stopped gateway receive queue: %v", err)
		}
		if peerWindow == 0 {
			t.Fatal("reliable fillers exhausted the peer window before a PR-TTL record could begin transmission")
		}
		if peerWindow <= windowTarget {
			break
		}
	}
	if len(fillerCorrelations) == 0 {
		t.Fatal("no reliable filler record reached the stopped association")
	}
	if peerWindow == 0 || peerWindow > windowTarget {
		t.Fatalf("stopped gateway receive window did not reach a small positive value: %d", peerWindow)
	}
	t.Logf("stopped gateway receive window reached %d after %d bounded reliable filler records", peerWindow, len(fillerCorrelations))

	sent, err := f.client.trySendRecord(targetWire, adapter.SCTPOrderedTTL, true)
	if err != nil || !sent {
		t.Fatalf("submit valid TTL request behind closed receive window: sent=%v err=%v", sent, err)
	}
	if err := waitSCTPPRAbandonment(t, f.client.fd, assoc, baseUnsent, baseSent, time.Now().Add(2*time.Second)); err != nil {
		t.Fatalf("prove native PR-TTL abandonment (peer rwnd=%d): %v", peerWindow, err)
	}
	_, _, err = sctpTestPRAssocStatus(f.client.fd, assoc)
	if err != nil {
		t.Fatalf("query PR-SCTP status after abandonment: %v", err)
	}

	if err := f.process.command.Process.Signal(syscall.SIGCONT); err != nil {
		t.Fatalf("resume gateway after native abandonment: %v", err)
	}
	stopped = false
	remaining := make(map[string]bool, len(fillerCorrelations))
	for correlation := range fillerCorrelations {
		remaining[correlation] = true
	}
	deadline := time.Now().Add(8 * time.Second)
	associationClosed := false
	for len(remaining) > 0 && time.Now().Before(deadline) {
		wire, readErr := f.client.readRecord(t, deadline)
		if readErr != nil {
			if errors.Is(readErr, os.ErrDeadlineExceeded) {
				break
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, unix.ECONNRESET) || errors.Is(readErr, unix.ECONNABORTED) {
				associationClosed = true
				break
			}
			t.Fatalf("read response while draining bounded reliable fillers: %v", readErr)
		}
		response := sctpDecodeResponse(t, f, wire, "")
		if !remaining[response.CorrelationID] || response.Event != "result" || response.Sequence != 1 || response.Result == nil || response.Result.Operation != core.CapabilitiesGet || response.Result.Capabilities == nil {
			t.Fatalf("unexpected response while draining reliable filler=%+v remaining=%d", response, len(remaining))
		}
		delete(remaining, response.CorrelationID)
	}
	if len(remaining) > 0 {
		t.Logf("partial-delivery abort or bounded drain ended with %d reliable filler replies unavailable", len(remaining))
	}
	if !associationClosed && time.Now().Before(deadline) {
		if _, err := f.client.readRecord(t, time.Now().Add(600*time.Millisecond)); err == nil {
			t.Fatal("expired PR-TTL request produced an additional gateway response")
		} else if !errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, io.EOF) && !errors.Is(err, unix.ECONNRESET) && !errors.Is(err, unix.ECONNABORTED) {
			t.Fatalf("read after draining filler replies: %v", err)
		}
	}
	assertSCTPNoNativeIntake(t, f)
	t.Logf("peer receiver window at expiry witness=%d; sender-side PR status proved the padded send was abandoned", peerWindow)

	// Retry the same authenticated envelope and correlation on a fresh
	// association. The signed intent remains stable across transport deliveries.
	if err := f.client.close(); err != nil {
		t.Errorf("close association after TTL expiry witness: %v", err)
	}
	select {
	case <-f.process.done:
		t.Fatalf("gateway exited after handling PR partial-delivery abort: %v; stderr=%s", f.process.err, f.process.stderr.String())
	default:
	}
	f.client = dialSCTP(t, f.address, adapter.SCTPOrderedTTL)
	if err := f.client.sendRecord(t, sctpRequestBytes(t, frame), time.Now().Add(5*time.Second)); err != nil {
		t.Fatalf("retry stable SCTP intent after reassociation: %v", err)
	}
	response := sctpReadResponse(t, f, f.client, frame.CorrelationID, time.Now().Add(8*time.Second))
	if response.Event != "result" || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != requestID {
		t.Fatalf("PR-TTL stable retry admission=%+v", response)
	}
	receipt := sctpWaitAccepted(t, f, f.client, requestID)
	seen := next(t, f.observer, func(event *irc.MessageEvent) bool { return event.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != request.Message {
		t.Fatalf("PR-TTL retry native event=%+v", seen)
	}
	messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name)
	if len(messages) != 1 || messages[0].ID != receipt.MessageID || messages[0].From != "fixture-author" || messages[0].Target != target.Name || messages[0].Message != request.Message || receipt.SendAttempts != 1 {
		t.Fatalf("PR-TTL retry native history=%+v receipt=%+v", messages, receipt)
	}
}
