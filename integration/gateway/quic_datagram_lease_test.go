//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestRawQUICQuietDatagramWatchExpiresWithContiguousSequence(t *testing.T) {
	f := newRawQUICFixture(t, true)
	socket := f.config.Upstream.Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	request := core.Request{Version: 1, RequestID: "quic-datagram-quiet", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	body := rawQUICFrame(t, rawQUICRequest(t, f, request, true, true))[4:]
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	for _, fragment := range webTransportDatagrams(body, id) {
		if err := f.conn.SendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	first := readRawQUICDatagramFrame(t, f, ctx)
	if first.Event != "page" || first.Sequence != 1 || first.Page == nil {
		t.Fatalf("raw QUIC quiet initial=%+v", first)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	expected := uint64(2)
	for {
		response := readRawQUICDatagramFrame(t, f, ctx)
		if response.Sequence != expected {
			t.Fatalf("raw QUIC quiet sequence=%d want=%d", response.Sequence, expected)
		}
		expected++
		if response.Event == "end" {
			if response.Reason != "lease_expired" {
				t.Fatalf("raw QUIC quiet terminal=%+v", response)
			}
			break
		}
		if response.Event != "page" || response.Page == nil || len(response.Page.Messages) != 0 || len(response.Page.Events) != 0 || response.Page.Cursor != first.Page.Cursor {
			t.Fatalf("raw QUIC quiet heartbeat=%+v", response)
		}
	}
	if elapsed := time.Since(started); elapsed < 13*time.Second || elapsed > 18*time.Second {
		t.Fatalf("raw QUIC quiet lease=%s", elapsed)
	}
	waitNativeObserverCountAtMost(t, statusClient, baseline)
	rawQUICDatagramCall(t, f, core.Request{Version: 1, RequestID: "quic-datagram-quiet-reuse", Operation: core.CapabilitiesGet}, true)
}
