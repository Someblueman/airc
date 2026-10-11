//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

const (
	webTransportDatagramHeader = 28
	webTransportDatagramChunk  = 972
)

func webTransportDatagrams(body []byte, id [16]byte) [][]byte {
	count := (len(body) + webTransportDatagramChunk - 1) / webTransportDatagramChunk
	fragments := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		start := index * webTransportDatagramChunk
		end := start + webTransportDatagramChunk
		if end > len(body) {
			end = len(body)
		}
		fragment := make([]byte, webTransportDatagramHeader+end-start)
		copy(fragment[:4], "AGD1")
		copy(fragment[4:20], id[:])
		binary.BigEndian.PutUint32(fragment[20:24], uint32(len(body)))
		binary.BigEndian.PutUint16(fragment[24:26], uint16(index))
		binary.BigEndian.PutUint16(fragment[26:28], uint16(count))
		copy(fragment[28:], body[start:end])
		fragments = append(fragments, fragment)
	}
	return fragments
}

func assembleWebTransportDatagrams(t *testing.T, fragments [][]byte) []byte {
	t.Helper()
	if len(fragments) == 0 {
		t.Fatal("empty WebTransport datagram response")
	}
	total := int(binary.BigEndian.Uint32(fragments[0][20:24]))
	count := int(binary.BigEndian.Uint16(fragments[0][26:28]))
	parts := make([][]byte, count)
	for _, fragment := range fragments {
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" || int(binary.BigEndian.Uint32(fragment[20:24])) != total {
			t.Fatalf("invalid response datagram=%x", fragment[:min(len(fragment), webTransportDatagramHeader)])
		}
		index := int(binary.BigEndian.Uint16(fragment[24:26]))
		if index >= count {
			t.Fatalf("response datagram index=%d count=%d", index, count)
		}
		parts[index] = append([]byte(nil), fragment[webTransportDatagramHeader:]...)
	}
	out := make([]byte, 0, total)
	for index, part := range parts {
		if part == nil {
			t.Fatalf("missing response datagram index=%d", index)
		}
		out = append(out, part...)
	}
	if len(out) != total {
		t.Fatalf("response datagram bytes=%d want=%d", len(out), total)
	}
	return out
}

func receiveWebTransportDatagramFrame(t *testing.T, f webTransportFixture, ctx context.Context) adapter.FrameResponse {
	t.Helper()
	fragments := make([][]byte, 0, 32)
	var count int
	for {
		fragment, err := f.session.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(fragment) < webTransportDatagramHeader {
			t.Fatalf("short response datagram: %d", len(fragment))
		}
		if count == 0 {
			count = int(binary.BigEndian.Uint16(fragment[26:28]))
		}
		fragments = append(fragments, fragment)
		if len(fragments) == count {
			break
		}
	}
	var response adapter.FrameResponse
	if err := jsonUnmarshalFrame(t, assembleWebTransportDatagrams(t, fragments), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestWebTransportDatagramRoundTripFragmented(t *testing.T) {
	f := newWebTransportFixture(t)
	request := webTransportRequest(t, f, "datagram", "datagram body "+strings.Repeat("x", 3500), true)
	body := webTransportFrame(t, request)[4:]
	var id [16]byte
	copy(id[:], "webtransport-test")
	fragments := webTransportDatagrams(body, id)
	for index := len(fragments) - 1; index >= 0; index-- {
		if err := f.session.SendDatagram(fragments[index]); err != nil {
			t.Fatal(err)
		}
		if index == len(fragments)-1 {
			if err := f.session.SendDatagram(fragments[index]); err != nil {
				t.Fatal(err)
			}
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	responseFragments := make([][]byte, 0, 32)
	for {
		fragment, err := f.session.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		responseFragments = append(responseFragments, fragment)
		if len(responseFragments) == int(binary.BigEndian.Uint16(fragment[26:28])) {
			break
		}
	}
	var response adapter.FrameResponse
	if data := assembleWebTransportDatagrams(t, responseFragments); len(data) == 0 || jsonUnmarshalFrame(t, data, &response) != nil {
		t.Fatal("invalid fragmented WebTransport response")
	}
	webTransportResponse(t, response, "datagram")
	if response.Sequence != 1 {
		t.Fatalf("basic datagram sequence=%d want 1", response.Sequence)
	}
	call := func(frame frameRequest) adapter.FrameResponse {
		t.Helper()
		frame.CorrelationID = "wt-" + rand.Text()
		var packetID [16]byte
		if _, err := rand.Read(packetID[:]); err != nil {
			t.Fatal(err)
		}
		for _, fragment := range webTransportDatagrams(webTransportFrame(t, frame)[4:], packetID) {
			if err := f.session.SendDatagram(fragment); err != nil {
				t.Fatal(err)
			}
		}
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		result := receiveWebTransportDatagramFrame(t, f, ctx)
		if result.CorrelationID != frame.CorrelationID || result.Event != "result" || result.Sequence != 1 || result.Error != nil || result.Result == nil {
			t.Fatal("datagram correlated result mismatch", result)
		}
		return result
	}
	// Discard the observed admission and retry the identical request under a new handle.
	if result := call(request).Result.Result; result == nil || !result.Durable {
		t.Fatal("datagram retry not durable", result)
	}
	var accepted *core.Result
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		result := call(adapterFrameRequest(t, f, core.Request{Version: 1, RequestID: "datagram-receipt", Operation: core.ReceiptGet, LookupID: "datagram"}, true)).Result.Result
		if result != nil && result.State == core.Accepted {
			accepted = result
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if accepted == nil || accepted.SendAttempts != 1 {
		t.Fatal("datagram retry duplicated or lost native acceptance", accepted)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if seen.From != "fixture-author" || seen.Target != "#gateway-fixture" || seen.Message != "datagram body "+strings.Repeat("x", 3500) {
		t.Fatal("datagram native observer mismatch", seen)
	}
	history := core.Request{Version: 1, RequestID: "datagram-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 1, Start: "retained"}
	page := call(adapterFrameRequest(t, f, history, true)).Result.Page
	if page == nil || len(page.Messages) != 1 || page.Messages[0].ID != seen.ID || page.Messages[0].Message != seen.Message || page.Messages[0].From != seen.From || page.Messages[0].Target != seen.Target || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("datagram bounded page mismatch", page)
	}
	history.Start, history.Cursor = "", page.Cursor
	empty := call(adapterFrameRequest(t, f, history, true)).Result.Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("datagram cursor resume mismatch", empty)
	}
}

func TestWebTransportDatagramMissingFragmentDoesNotDispatch(t *testing.T) {
	f := newWebTransportFixture(t)
	body := webTransportFrame(t, webTransportRequest(t, f, "datagram-missing", strings.Repeat("missing fragment ", 100), true))[4:]
	var id [16]byte
	copy(id[:], "webtransport-miss")
	fragments := webTransportDatagrams(body, id)
	if len(fragments) < 2 {
		t.Fatal("missing fragment control must be multipart")
	}
	if err := f.session.SendDatagram(fragments[0]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer cancel()
	if _, err := f.session.ReceiveDatagram(ctx); err == nil {
		t.Fatal("missing WebTransport datagram fragment dispatched a response")
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestWebTransportDatagramWatchSequencesRetainedAndLive(t *testing.T) {
	f := newWebTransportFixture(t)
	parentID := seedPinMutationMessage(t, f.config.Upstream.Address, "#gateway-fixture", "datagram-watch-parent")
	author := dial(t, f.config.Upstream.Address, "webtransport-datagram-author", "", false, true)
	t.Cleanup(func() { _ = author.Close() })
	request := core.Request{Version: 1, RequestID: "datagram-watch", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	frame := adapterFrameRequest(t, f, request, true)
	frame.Stream = true
	body := webTransportFrame(t, frame)[4:]
	var id [16]byte
	copy(id[:], "datagram-watch-id")
	for _, fragment := range webTransportDatagrams(body, id) {
		if err := f.session.SendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	initial := receiveWebTransportDatagramFrame(t, f, ctx)
	if initial.Sequence != 1 || initial.Page == nil || len(initial.Page.Messages) == 0 || initial.Page.Messages[0].ID != parentID {
		t.Fatalf("datagram retained sequence/page=%d/%+v", initial.Sequence, initial.Page)
	}
	triggerLiveSignal(t, author, "typing")
	live := receiveWebTransportDatagramFrame(t, f, ctx)
	if live.Sequence != 2 || live.Page == nil || live.Page.Cursor != initial.Page.Cursor || len(live.Page.Events) == 0 {
		t.Fatalf("datagram live sequence/page=%d/%+v", live.Sequence, live.Page)
	}
}

func jsonUnmarshalFrame(t *testing.T, data []byte, target any) error {
	t.Helper()
	if len(data) > adapter.MaxResponseFrameBytes {
		return fmt.Errorf("response exceeds bound: %d", len(data))
	}
	return json.Unmarshal(data, target)
}
