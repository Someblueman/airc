//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUDPLostResponseReplayReorderAndSourceReconnect(t *testing.T) {
	var proxy *udpPacketProxy
	f := newUDPFixtureWithReturnRoute(t, func(route *config.ReturnRoute, mailbox *net.UDPConn) {
		proxy = newUDPPacketProxy(t, mailbox)
		route.Destination = proxy.conn.LocalAddr().String()
	})
	if proxy == nil {
		t.Fatal("UDP proxy was not initialized before gateway start")
	}
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued := time.Now().Unix()
	expires := issued + 120
	lost := core.Request{Version: 1, RequestID: "udp-lost-response", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: "UDP response intentionally lost"}
	lostToken := f.credentials.validityToken(t, lost, issued, expires, true)
	writeUDPTokenFrame(t, f, "udp-loss-transport-1", lostToken)
	select {
	case <-proxy.dropDone:
	case <-time.After(3 * time.Second):
		t.Fatal("proxy did not observe a real dropped response")
	}
	expectUDPProxyDrop(t, f)

	// The same signed intent is replayed with a fresh transport correlation
	// and AGD1 ID; acceptance must remain one durable native message.
	writeUDPTokenFrame(t, f, "udp-loss-transport-2", lostToken)
	replayed := readUDPResponse(t, f, time.Now().Add(5*time.Second))
	if replayed.Result == nil || replayed.Result.Result == nil || replayed.Result.Result.RequestID != lost.RequestID || !replayed.Result.Result.Durable {
		t.Fatalf("UDP replay result=%+v", replayed)
	}
	receipt := pollUDPReceipt(t, f, lost.RequestID)
	if receipt.SendAttempts != 1 || receipt.MessageID == "" {
		t.Fatalf("UDP replay receipt=%+v", receipt)
	}
	lostMessageID := receipt.MessageID
	if replayed.Result.Result.MessageID != "" && replayed.Result.Result.MessageID != lostMessageID {
		t.Fatalf("UDP authoritative replay ID=%q initial=%q", replayed.Result.Result.MessageID, lostMessageID)
	}

	proxy.setReorderNext()
	largeIssued := time.Now().Unix()
	largeExpires := largeIssued + 120
	large := core.Request{Version: 1, RequestID: "udp-reordered-message", IssuedAt: &largeIssued, ExpiresAt: &largeExpires, Operation: core.Send, Target: room, Message: "UDP reordered body " + strings.Repeat("x", 3500)}
	largeToken := f.credentials.validityToken(t, large, largeIssued, largeExpires, true)
	requestFragments, requestDuplicated := writeUDPTokenFrameControlled(t, f, "udp-reordered-transport", largeToken)
	if requestFragments < 2 || !requestDuplicated {
		t.Fatalf("UDP request controls fragments=%d duplicated=%t", requestFragments, requestDuplicated)
	}
	largeResponse := readUDPResponse(t, f, time.Now().Add(5*time.Second))
	if largeResponse.Result == nil || largeResponse.Result.Result == nil || largeResponse.Result.Result.RequestID != large.RequestID {
		t.Fatalf("UDP reordered result=%+v", largeResponse)
	}
	// The proxy duplicated one original response fragment. Consume that
	// duplicate before issuing the independent receipt request.
	if err := f.mailbox.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	wireDuplicate := make([]byte, 1001)
	n, _, err := f.mailbox.ReadFromUDP(wireDuplicate)
	if err != nil {
		t.Fatalf("duplicate response fragment missing: %v", err)
	}
	dropped, reordered, duplicated := proxy.stats()
	original, _ := proxy.duplicateBytes()
	if dropped < 1 || reordered != 1 || duplicated != 1 || len(original) == 0 || !bytes.Equal(original, wireDuplicate[:n]) {
		t.Fatalf("UDP wire controls dropped=%d reordered=%d duplicated=%d wire_equal=%t", dropped, reordered, duplicated, bytes.Equal(original, wireDuplicate[:n]))
	}
	largeReceipt := pollUDPReceipt(t, f, large.RequestID)

	partialIssued := time.Now().Unix()
	partialExpires := partialIssued + 120
	partial := core.Request{Version: 1, RequestID: "udp-partial-expiry", IssuedAt: &partialIssued, ExpiresAt: &partialExpires, Operation: core.Send, Target: room, Message: "UDP partial request " + strings.Repeat("p", 3500)}
	partialToken := f.credentials.validityToken(t, partial, partialIssued, partialExpires, true)
	partialFragments := writeUDPTokenFramePartial(t, f, "udp-partial-transport", partialToken)
	if len(partialFragments) < 2 {
		t.Fatalf("partial request fragments=%d", len(partialFragments))
	}
	expectUDPProxyDrop(t, f)
	time.Sleep(6 * time.Second)
	// Completing the original AGD1 ID after its deadline must not resurrect
	// the expired first fragment. A retry with a fresh ID follows below.
	if err := f.source.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range partialFragments[1:] {
		if _, err := f.source.WriteToUDP(fragment, mustUDPAddress(t, f.address)); err != nil {
			t.Fatal(err)
		}
	}
	expectUDPProxyDrop(t, f)
	beforePartialRetry := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(beforePartialRetry) != 2 || beforePartialRetry[0].Message != lost.Message || beforePartialRetry[1].Message != large.Message {
		t.Fatalf("partial request changed native state before retry=%+v", beforePartialRetry)
	}
	partialAccepted := udpDatagramAccepted(t, f, partial, true)
	if partialAccepted.MessageID == "" || partialAccepted.SendAttempts != 1 {
		t.Fatalf("partial request retry=%+v", partialAccepted)
	}

	_ = f.source.Close()
	source, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	f.source = source
	t.Cleanup(func() { _ = source.Close() })
	reconnected := udpDatagramAccepted(t, f, large, true)
	if reconnected.MessageID != largeReceipt.MessageID || reconnected.SendAttempts != 1 {
		t.Fatalf("UDP reconnect replay=%+v original=%+v", reconnected, largeReceipt)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 3 || native[0].ID != lostMessageID || native[0].Message != lost.Message || native[0].From != "fixture-author" || native[1].ID != largeReceipt.MessageID || native[1].Message != large.Message || native[1].From != "fixture-author" || native[2].ID != partialAccepted.MessageID || native[2].Message != partial.Message || native[2].From != "fixture-author" || native[0].Target != room.Name || native[1].Target != room.Name || native[2].Target != room.Name {
		t.Fatalf("UDP native durable facts=%+v", native)
	}
	history := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-replay-history", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 8}, true)
	if history.Result == nil || history.Result.Page == nil || !reflect.DeepEqual(history.Result.Page.Messages, native) {
		t.Fatalf("UDP history/native mismatch history=%+v native=%+v", history.Result, native)
	}
	bounded := core.Request{Version: 1, RequestID: "udp-bounded", Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
	for i, message := range native {
		response := udpDatagramCall(t, f, bounded, true)
		if response.Result == nil {
			t.Fatal("UDP bounded read omitted result", response)
		}
		page := response.Result.Page
		want := "more"
		if i == len(native)-1 {
			want = "ok"
		}
		if page == nil || len(page.Messages) != 1 || !reflect.DeepEqual(page.Messages[0], message) || page.Status != want || page.Cursor == "" || page.Gap {
			t.Fatal("UDP bounded mailbox page mismatch", page)
		}
		bounded.Start, bounded.Cursor = "", page.Cursor
	}
	response := udpDatagramCall(t, f, bounded, true)
	if response.Result == nil {
		t.Fatal("UDP resume omitted result", response)
	}
	empty := response.Result.Page
	if empty == nil || len(empty.Messages) != 0 || empty.Status != "ok" || empty.Cursor == "" || empty.Gap {
		t.Fatal("UDP caught-up mailbox resume mismatch", empty)
	}

}

func pollUDPReceipt(t *testing.T, f udpFixture, id string) core.Result {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	var result core.Result
	for attempt := 0; attempt < 4 && time.Now().Before(deadline); attempt++ {
		response := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", id, attempt), Operation: core.ReceiptGet, LookupID: id}, true)
		if response.Result == nil || response.Result.Result == nil || response.Result.Result.RequestID != id {
			t.Fatalf("UDP receipt response=%+v", response)
		}
		result = *response.Result.Result
		if result.State == core.Accepted {
			return result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("UDP receipt did not reach acceptance: %+v", result)
	return core.Result{}
}
