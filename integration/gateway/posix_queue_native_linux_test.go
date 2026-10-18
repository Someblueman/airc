//go:build linux && (arm64 || amd64)

package gateway_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/testmqueue"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestPOSIXQueueNativeUnicodeCursorAndReplay(t *testing.T) {
	testmqueue.Require(t)
	f := newPOSIXFixture(t)
	target := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	for i, body := range []string{"POSIX queue Καλημέρα 世界 🛰️", "POSIX fragments " + strings.Repeat("λ", 1400)} {
		issued, expiry := time.Now().Unix(), time.Now().Unix()+120
		request := core.Request{Version: 1, RequestID: []string{"pq-unicode", "pq-fragments"}[i], Operation: core.Send, Target: target, Message: body, IssuedAt: &issued, ExpiresAt: &expiry}
		if i == 1 {
			frame := posixFrame(t, f, request, false)
			parts := posixFragments(t, frame)
			if len(parts) < 2 || len(parts[0]) != 1000 {
				t.Fatal("fragment witness missing")
			}
			for index := len(parts) - 1; index >= 0; index-- {
				writePOSIXFragments(t, f, parts[index:index+1])
			}
			// Discard the actual response; the producer retries its retained intent.
			lost := readAGD1Payload(t, &posixResponseReader{queue: f.mailbox}, time.Now().Add(5*time.Second))
			if len(lost) == 0 {
				t.Fatal("response loss witness missing")
			}
		}
		receipt := posixAccepted(t, f, request)
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
	request := core.Request{Version: 1, RequestID: "pq-history", Operation: core.HistoryRead, Target: target, Limit: 1, Start: "retained"}
	var collected []*irc.MessageMetadata
	complete := false
	for i := 0; i < 4; i++ {
		page := posixCall(t, f, request).Result.Page
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
