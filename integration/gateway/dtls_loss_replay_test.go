//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestDTLSPacketProxyDropsClientHelloAndForwards(t *testing.T) {
	f := newDTLSFixture(t)
	_ = f.conn.Close()
	p := newDTLSPacketProxy(t, f.address, true)
	f.conn = dialDTLSProxy(t, f, p)
	request := core.Request{Version: 1, RequestID: "dtls-proxy-handshake", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "actual DTLS handshake retransmission"}
	result := dtlsDatagramAccepted(t, f, request, true)
	dropped, _, _, _ := p.stats()
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if !dropped || len(native) != 1 || native[0].ID != result.MessageID || native[0].Message != request.Message || native[0].From != "fixture-author" {
		t.Fatalf("DTLS lost handshake/native facts dropped=%t native=%+v result=%+v", dropped, native, result)
	}
}

func TestDTLSActualResponseLossReorderDuplicateAndReconnectReplay(t *testing.T) {
	f := newDTLSFixture(t)
	_ = f.conn.Close()
	p := newDTLSPacketProxy(t, f.address, false)
	f.conn = dialDTLSProxy(t, f, p)
	p.setDropServerApplication(true)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued := time.Now().Unix()
	expires := issued + 120
	lost := core.Request{Version: 1, RequestID: "dtls-lost-response", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: "DTLS accepted response intentionally lost"}
	writeDTLSFrame(t, f, adapter.FrameRequest{Version: 1, CorrelationID: "dtls-loss-initial", Profile: "encrypted", Envelope: f.credentials.token(t, lost, true)})
	select {
	case <-p.serverDrop:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not observe/drop a real server application response")
	}
	_ = f.conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := f.conn.Read(make([]byte, 1001)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("response loss was not an uncertain timeout: %v", err)
	}
	// Cross the signing clock boundary deliberately. Rebuilding an envelope
	// without retaining these original validity fields changes the durable
	// intent and correctly produces id_conflict.
	clockDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Unix() <= issued && time.Now().Before(clockDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if time.Now().Unix() <= issued {
		t.Fatal("clock did not advance across the replay control boundary")
	}
	p.setDropServerApplication(false)
	// Replay the same durable intent over the still-live association. Transport
	// frame/correlation IDs are fresh; a timeout did not establish non-acceptance.
	recovered := dtlsDatagramAccepted(t, f, lost, true)
	reordered := core.Request{Version: 1, RequestID: "dtls-reordered-new-intent", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: "DTLS reordered " + strings.Repeat("x", 4000)}
	p.setReorderClientApplication()
	second := dtlsDatagramAccepted(t, f, reordered, true)
	_, dropped, swapped, duplicated := p.stats()
	if dropped < 1 || swapped != 1 || duplicated != 1 {
		t.Fatalf("wire controls dropped=%d swapped=%d duplicated=%d", dropped, swapped, duplicated)
	}
	_ = f.conn.Close()
	p.close()
	p2 := newDTLSPacketProxy(t, f.address, false)
	f.conn = dialDTLSProxy(t, f, p2)
	replay := dtlsDatagramAccepted(t, f, reordered, true)
	if replay.MessageID != second.MessageID || replay.RequestID != reordered.RequestID || recovered.SendAttempts != 1 || second.SendAttempts != 1 || replay.SendAttempts != 1 {
		t.Fatalf("reconnect replay=%+v first=%+v", replay, second)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 2 || native[0].ID != recovered.MessageID || native[0].Message != lost.Message || native[1].ID != second.MessageID || native[1].Message != reordered.Message || native[0].From != "fixture-author" || native[1].From != "fixture-author" || native[0].Target != room.Name || native[1].Target != room.Name {
		t.Fatalf("DTLS exact replay/native facts=%+v", native)
	}
	response := dtlsDatagramCall(t, f, core.Request{Version: 1, RequestID: "dtls-proxy-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8}, true)
	if response.Result.Page == nil || !reflect.DeepEqual(response.Result.Page.Messages, native) {
		t.Fatal("proxied DTLS history differs from complete native CHECK records")
	}
	bounded := core.Request{Version: 1, RequestID: "dtls-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, message := range native {
		response := dtlsDatagramCall(t, f, bounded, true)
		if response.Result == nil {
			t.Fatal("DTLS bounded read omitted result", response)
		}
		page := response.Result.Page
		want := "more"
		if i == len(native)-1 {
			want = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], message) || page.Status != want || page.Cursor == "" || page.Gap {
			t.Fatal("DTLS bounded mailbox page mismatch", page)
		}
		bounded.Start, bounded.Cursor = "", page.Cursor
	}
	response = dtlsDatagramCall(t, f, bounded, true)
	if response.Result == nil {
		t.Fatal("DTLS resume omitted result", response)
	}
	empty := response.Result.Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("DTLS caught-up mailbox resume mismatch", empty)
	}

}
