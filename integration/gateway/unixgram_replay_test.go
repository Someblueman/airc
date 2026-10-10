//go:build darwin || linux

package gateway_test

import (
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestUnixgramResponseLossStableReplayAndSourceReconnect(t *testing.T) {
	f := newUnixgramFixture(t)
	issued, expiry := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "ug-lost-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "unixgram lost response " + strings.Repeat("x", 3500), IssuedAt: &issued, ExpiresAt: &expiry}
	frame := unixgramFrame(t, f, request, false)
	fragments := unixgramFragments(t, frame)
	if len(fragments) < 2 || len(fragments[0]) != 1000 {
		t.Fatal("reorder witness lacks a full 1000-byte fragment")
	}
	// Deliver all actual fragments backwards, including a duplicate.
	for i := len(fragments) - 1; i >= 0; i-- {
		writeUnixgramFragments(t, f, fragments[i:i+1])
		if i == len(fragments)-1 {
			writeUnixgramFragments(t, f, fragments[i:i+1])
		}
	}
	// Receive and deliberately discard the actual encrypted response: the
	// retrying caller has no admission result or receipt from this attempt.
	lost := readAGD1Payload(t, f.mailbox, time.Now().Add(5*time.Second))
	if len(lost) == 0 {
		t.Fatal("loss witness did not receive a response")
	}
	if err := f.source.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(f.address), "reconnected")
	source, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = source.Close() })
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	f.source = source
	frame.CorrelationID = "ug-replay-new-correlation"
	writeUnixgramFragments(t, f, unixgramFragments(t, frame))
	replayed := readUnixgramResponse(t, f, time.Now().Add(5*time.Second))
	if replayed.CorrelationID != frame.CorrelationID || replayed.Result == nil || replayed.Result.Result == nil || !replayed.Result.Result.Durable || replayed.Result.Result.RequestID != request.RequestID {
		t.Fatalf("Unix datagram replay=%+v error=%+v", replayed, replayed.Error)
	}
	var receipt core.Result
	for deadline, attempt := time.Now().Add(5*time.Second), 0; time.Now().Before(deadline); attempt++ {
		response := unixgramCall(t, f, core.Request{Version: 1, RequestID: "ug-replay-receipt-" + string(rune('a'+attempt%26)), Operation: core.ReceiptGet, LookupID: request.RequestID}, true)
		if response.Result.Result == nil {
			t.Fatalf("Unix datagram replay receipt=%+v", response)
		}
		receipt = *response.Result.Result
		if receipt.State == core.Accepted {
			break
		}
		time.Sleep(40 * time.Millisecond)
	}
	if receipt.State != core.Accepted || receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatalf("Unix datagram replay receipt=%+v", receipt)
	}
	seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == receipt.MessageID })
	if seen.From != "fixture-author" || seen.Target != request.Target.Name || seen.Message != request.Message {
		t.Fatal("Unix datagram observer mismatch", seen)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 1 || native[0].ID != receipt.MessageID || native[0].Message != request.Message || native[0].From != "fixture-author" {
		t.Fatalf("Unix datagram replay native facts=%+v", native)
	}
	history := core.Request{Version: 1, RequestID: "ug-bounded-history", Operation: core.HistoryRead, Target: request.Target, Limit: 1, Start: "retained"}
	page := unixgramCall(t, f, history, true).Result.Page
	if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], native[0]) || page.Status != "ok" || page.Cursor == "" || page.Gap {
		t.Fatal("Unix datagram bounded mailbox page mismatch", page)
	}
	history.Start, history.Cursor = "", page.Cursor
	empty := unixgramCall(t, f, history, true).Result.Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("Unix datagram mailbox cursor resume mismatch", empty)
	}
}
