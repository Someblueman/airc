//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func exercisePacketNativeUnicodeCursorAndReplay(t *testing.T, f udpFixture, label string) {
	t.Helper()
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for i, body := range []string{label + " Καλημέρα 世界 🛰️", label + " fragments " + strings.Repeat("λ", 1400)} {
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		request := core.Request{Version: 1, RequestID: []string{label + "-unicode", label + "-fragments"}[i], Operation: core.Send, Target: target, Message: body, IssuedAt: &issued, ExpiresAt: &expiry}
		if i == 1 {
			frame := frameRequest{Version: 1, CorrelationID: "udp-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)}
			encoded, err := json.Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			parts := webTransportDatagrams(encoded, [16]byte{9})
			if len(parts) < 2 || len(parts[0]) != 1000 {
				t.Fatal("fragment witness missing")
			}
			if err := f.source.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
				t.Fatal(err)
			}
			for index := len(parts) - 1; index >= 0; index-- {
				if _, err := f.source.WriteToUDP(parts[index], mustUDPAddress(t, f.address)); err != nil {
					t.Fatal(err)
				}
			}
			// Discard the actual response; the producer retries its retained intent.
			lost := readUDPResponse(t, f, time.Now().Add(5*time.Second))
			if lost.CorrelationID != frame.CorrelationID || lost.Event != "result" || lost.Result == nil || lost.Result.Result == nil || lost.Result.Result.RequestID != request.RequestID || !lost.Result.Result.Durable {
				t.Fatal("response loss witness missing")
			}
		}
		receipt := udpDatagramAccepted(t, f, request, true)
		if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
			t.Fatalf("receipt=%+v", receipt)
		}
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
		if seen.From != "fixture-author" || seen.Target != target.Name || seen.Message != body {
			t.Fatal("native observer mismatch", seen)
		}
	}
	native := nativeWebTransportMessages(t, f.config.Upstream.Address, target.Name)
	if len(native) != 2 {
		t.Fatalf("expected two native messages, got %d", len(native))
	}
	request := core.Request{Version: 1, RequestID: label + "-history", Operation: core.HistoryRead, Target: target, Limit: 1, Start: "retained"}
	var collected []*irc.MessageMetadata
	complete := false
	for i := 0; i < 4; i++ {
		page := udpDatagramCall(t, f, request, true).Result.Page
		if page == nil || (page.Status != "ok" && page.Status != "more") || page.Gap || page.Cursor == "" || len(page.Messages) > 1 {
			t.Fatalf("bounded page=%+v", page)
		}
		collected = append(collected, page.Messages...)
		request.Start, request.Cursor = "", page.Cursor
		if len(page.Messages) == 0 {
			if page.Status != "ok" {
				t.Fatal("empty final page is not complete", page.Status)
			}
			complete = true
			break
		}
	}
	if !complete || !reflect.DeepEqual(collected, native) {
		t.Fatal("cursor history differs from independent native read", collected, native)
	}
}
