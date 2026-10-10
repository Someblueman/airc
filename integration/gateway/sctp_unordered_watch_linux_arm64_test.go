//go:build linux && arm64

package gateway_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestSCTPUnorderedQuietWatchReassemblesProtectedSequence(t *testing.T) {
	runSCTPQuietWatchReassemblesProtectedSequence(t, adapter.SCTPUnordered, "sctp_unordered_records")
}

func TestSCTPPRTTLQuietWatchReassemblesProtectedSequence(t *testing.T) {
	runSCTPQuietWatchReassemblesProtectedSequence(t, adapter.SCTPOrderedTTL, "sctp_pr_ttl_records")
}

func runSCTPQuietWatchReassemblesProtectedSequence(t *testing.T, mode adapter.SCTPMode, watchMode string) {
	t.Helper()
	f := newSCTPFixtureMode(t, "ipv4", mode, true)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "sctp-unordered-watch-seed", Operation: core.Send, Target: target, Message: "retained unordered watch entry 世界", IssuedAt: &issued, ExpiresAt: &expiry}
	accepted := sctpAccepted(t, f, f.client, request)
	seed := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if seed.From != "fixture-author" || seed.Target != target.Name || seed.Message != request.Message {
		t.Fatalf("unordered watch seed=%+v", seed)
	}
	retained := nativeWebTransportMessages(t, f.config.Bindings[0].Address, target.Name)
	if len(retained) != 1 || retained[0].ID != accepted.MessageID {
		t.Fatalf("unordered watch retained native page=%+v", retained)
	}

	watch := core.Request{Version: 1, RequestID: "sctp-unordered-quiet-watch", Operation: core.Watch, Target: target, Start: "retained", Limit: 8}
	frame := sctpFrame(t, f, watch, true)
	frame.Stream = true
	started := time.Now()
	sctpSendFrame(t, f.client, frame)
	deadline := time.Now().Add(25 * time.Second)
	responses := make(map[uint64]adapter.FrameResponse, 129)
	terminal := uint64(0)
	totalWireBytes := 0
	for terminal == 0 || uint64(len(responses)) < terminal {
		if len(responses) >= 129 {
			t.Fatal("unordered quiet watch exceeded 128 pages plus terminal")
		}
		response, wireBytes := sctpReadAnyResponse(t, f, f.client, deadline)
		if response.CorrelationID != frame.CorrelationID || response.Sequence == 0 || response.Sequence > 129 {
			t.Fatalf("unordered quiet watch correlation/sequence=%q/%d", response.CorrelationID, response.Sequence)
		}
		if _, duplicate := responses[response.Sequence]; duplicate {
			t.Fatalf("unordered quiet watch repeated sequence %d", response.Sequence)
		}
		totalWireBytes += wireBytes
		if totalWireBytes > 1<<20 {
			t.Fatalf("unordered quiet watch sealed bytes=%d exceeds 1 MiB", totalWireBytes)
		}
		if terminal != 0 && response.Sequence > terminal {
			t.Fatalf("unordered quiet watch received sequence %d after terminal %d", response.Sequence, terminal)
		}
		responses[response.Sequence] = response
		if response.Event == "end" {
			if response.Reason != "lease_expired" || terminal != 0 {
				t.Fatalf("unordered quiet watch terminal=%+v", response)
			}
			terminal = response.Sequence
		} else if response.Event != "page" || response.Page == nil || response.Error != nil {
			t.Fatalf("unordered quiet watch record=%+v", response)
		}
	}
	if terminal < 2 || len(responses) != int(terminal) {
		t.Fatalf("unordered quiet watch terminal=%d records=%d", terminal, len(responses))
	}
	if elapsed := time.Since(started); elapsed < 13*time.Second || elapsed > 18*time.Second {
		t.Fatalf("unordered quiet watch lease=%s", elapsed)
	}
	for sequence := uint64(1); sequence <= terminal; sequence++ {
		if _, ok := responses[sequence]; !ok {
			t.Fatalf("unordered quiet watch missing sequence %d of terminal %d", sequence, terminal)
		}
	}
	first := responses[1]
	if first.Event != "page" || first.Page == nil || len(first.Page.Messages) != len(retained) || !reflect.DeepEqual(first.Page.Messages, retained) || first.Page.Cursor == "" {
		t.Fatalf("unordered quiet watch initial page=%+v want=%+v", first.Page, retained)
	}
	codec, err := auth.NewCursorCodec(f.config.CursorKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	principal := f.config.Principals[0]
	scope := auth.CursorScope{Realm: f.config.Realm, Principal: principal.ID, Binding: principal.Binding, Operation: core.Watch, Target: *target}
	initialPosition, err := codec.Open(scope, first.Page.Cursor)
	if err != nil {
		t.Fatalf("open unordered quiet initial cursor: %v", err)
	}
	for sequence := uint64(2); sequence < terminal; sequence++ {
		response := responses[sequence]
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 || response.Page.Cursor == "" {
			t.Fatalf("unordered quiet heartbeat sequence=%d response=%+v", sequence, response)
		}
		position, err := codec.Open(scope, response.Page.Cursor)
		if err != nil || position != initialPosition {
			t.Fatalf("unordered quiet heartbeat cursor sequence=%d position=%q want=%q err=%v", sequence, position, initialPosition, err)
		}
	}
	capabilities := sctpCall(t, f, f.client, core.Request{Version: 1, RequestID: "sctp-unordered-watch-reuse", Operation: core.CapabilitiesGet})
	if capabilities.Result == nil || capabilities.Result.Capabilities == nil || !reflect.DeepEqual(capabilities.Result.Capabilities.WatchModes, []string{"long_poll", watchMode}) {
		t.Fatalf("%s quiet watch association reuse/capabilities=%+v", watchMode, capabilities)
	}
}
