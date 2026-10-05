//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func readRawQUICDatagramFrame(t *testing.T, f rawQUICFixture, ctx context.Context) adapter.FrameResponse {
	t.Helper()
	var fragments [][]byte
	count := 0
	for {
		fragment, err := f.conn.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" {
			t.Fatalf("invalid raw QUIC datagram response length=%d", len(fragment))
		}
		if count == 0 {
			count = int(binary.BigEndian.Uint16(fragment[26:28]))
		}
		fragments = append(fragments, fragment)
		if len(fragments) == count {
			break
		}
	}
	body := assembleWebTransportDatagrams(t, fragments)
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func rawQUICDatagramCall(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	return rawQUICDatagramCallWithOrder(t, f, request, encrypted, false)
}

func rawQUICDatagramCallWithOrder(t *testing.T, f rawQUICFixture, request core.Request, encrypted, reorder bool) adapter.FrameResponse {
	t.Helper()
	body := rawQUICFrame(t, rawQUICRequest(t, f, request, encrypted, false))[4:]
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(body, id)
	if reorder {
		for i := len(fragments) - 1; i >= 0; i-- {
			if err := f.conn.SendDatagram(fragments[i]); err != nil {
				t.Fatal(err)
			}
			if i == len(fragments)-1 {
				if err := f.conn.SendDatagram(fragments[i]); err != nil {
					t.Fatal(err)
				}
			}
		}
	} else {
		for _, fragment := range fragments {
			if err := f.conn.SendDatagram(fragment); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	return readRawQUICDatagramFrame(t, f, ctx)
}

func rawQUICDatagramAccepted(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	initial := rawQUICAssertResult(t, rawQUICDatagramCall(t, f, request, encrypted), request.RequestID)
	if !initial.Durable {
		t.Fatalf("raw QUIC datagram admission=%+v", initial)
	}
	var last core.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		receiptID := request.RequestID + "-receipt"
		response := rawQUICDatagramCall(t, f, core.Request{Version: 1, RequestID: receiptID, Operation: core.ReceiptGet, LookupID: request.RequestID}, encrypted)
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != request.RequestID {
			t.Fatalf("raw QUIC datagram receipt response=%+v", response)
		}
		last = *response.Result.Result
		if last.State == core.Accepted {
			return last
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("raw QUIC datagram request did not reach accepted: %+v", last)
	return core.Result{}
}

func TestRawQUICDatagramFragmentReplayAndNativeFact(t *testing.T) {
	f := newRawQUICFixture(t, true)
	// Validity is part of the signed mutation intent. Keep it fixed across
	// replay instead of generating a conflicting intent at a second boundary.
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "quic-datagram-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "raw QUIC datagram " + strings.Repeat("x", 3500), IssuedAt: &issued, ExpiresAt: &expiry}
	initial := rawQUICAssertResult(t, rawQUICDatagramCallWithOrder(t, f, request, true, true), request.RequestID)
	if !initial.Durable {
		t.Fatalf("raw QUIC datagram admission=%+v", initial)
	}
	result := rawQUICDatagramAccepted(t, f, request, true)
	if result.MessageID == "" {
		t.Fatalf("raw QUIC datagram result=%+v", result)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 1 || native[0].ID != result.MessageID || native[0].Message != request.Message || native[0].From != "fixture-author" || native[0].Target != request.Target.Name {
		t.Fatalf("raw QUIC datagram replay/native facts=%+v", native)
	}
}

func TestRawQUICDatagramMissingFragmentDoesNotAdmit(t *testing.T) {
	f := newRawQUICFixture(t, true)
	request := core.Request{Version: 1, RequestID: "quic-datagram-missing", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: strings.Repeat("x", 3500)}
	before := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	fragments := webTransportDatagrams(rawQUICFrame(t, rawQUICRequest(t, f, request, true, false))[4:], [16]byte{'m', 'i', 's', 's', 'i', 'n', 'g'})
	if len(fragments) < 2 {
		t.Fatal("test payload did not fragment")
	}
	if err := f.conn.SendDatagram(fragments[0]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 1500*time.Millisecond)
	defer cancel()
	if _, err := f.conn.ReceiveDatagram(ctx); err == nil {
		t.Fatal("raw QUIC incomplete datagram unexpectedly produced a response")
	}
	after := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("raw QUIC incomplete datagram changed native history: before=%+v after=%+v", before, after)
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestRawQUICDatagramWatchRetainedLiveAndConnectionCancellation(t *testing.T) {
	f := newRawQUICFixture(t, true)
	socket := f.config.Bindings[0].Address
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	parent := seedPinMutationMessage(t, socket, room.Name, "quic-datagram-watch-parent")
	baseline := nativeObserverCount(t, socket)
	request := core.Request{Version: 1, RequestID: "quic-datagram-watch", Operation: core.Watch, Target: room, Start: "retained", Limit: 8}
	body := rawQUICFrame(t, rawQUICRequest(t, f, request, true, true))[4:]
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range webTransportDatagrams(body, id) {
		if err := f.conn.SendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	retained := readRawQUICDatagramFrame(t, f, ctx)
	if retained.Event != "page" || retained.Sequence != 1 || retained.Page == nil || len(retained.Page.Messages) == 0 || retained.Page.Messages[0].ID != parent {
		t.Fatalf("raw QUIC datagram retained watch=%+v", retained)
	}
	waitNativeObserverCount(t, socket, baseline+1)
	author := dial(t, socket, "quic-datagram-watch-author", "", false, true)
	if err := author.SendWithID(room.Name, "raw QUIC datagram live 🙂", "quic-datagram-watch-live"); err != nil {
		t.Fatal(err)
	}
	_ = author.Close()
	live := readRawQUICDatagramFrame(t, f, ctx)
	if live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) == 0 || live.Page.Messages[len(live.Page.Messages)-1].Message != "raw QUIC datagram live 🙂" {
		t.Fatalf("raw QUIC datagram live watch=%+v", live)
	}
	_ = f.conn.CloseWithError(0, "cancel datagram watch")
	waitNativeLeaseGone(t, socket, baseline)
}
