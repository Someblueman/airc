//go:build darwin || linux

package gateway_test

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

func TestOSCOREOutgoingTransferCapAndAbsoluteExpiry(t *testing.T) {
	f := newOSCOREFixture(t)
	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	seed := core.Request{Version: 1, RequestID: "oscore-transfer-seed", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: "held response 🙂" + strings.Repeat("h", 3500)}
	_ = oscoreAccepted(t, f, seed)
	data, err := os.ReadFile(f.sequence)
	if err != nil {
		t.Fatal(err)
	}
	base, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	relays := make([]*oscoreCapture, 4)
	var first, last time.Time
	for i := range relays {
		relay := captureOSCORE(t, f.address)
		relay.holdResponse()
		relays[i] = relay
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("oscore-held-%d", i), Operation: core.HistoryRead, Target: room, Start: "retained", Limit: 1}
		// Disjoint eight-nonce ranges match the pinned client's reservation frequency.
		// Their total span stays within the server's 64-entry replay window.
		command := oscoreTransferCommand(t, f, request, relay.front.LocalAddr().String(), base+uint64(8*i))
		startHeldOSCOREClient(t, command)
		last = oscoreResponseTime(t, relay)
		if i == 0 {
			first = last
		}
	}
	if last.Sub(first) > time.Second {
		t.Fatal("fixture could not fill response cap inside absolute transfer lifetime")
	}
	refused := core.Request{Version: 1, RequestID: "oscore-transfer-refused", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: room, Message: "admitted only after absolute response expiration"}
	fifthRelay := captureOSCORE(t, f.address)
	command := oscoreTransferCommand(t, f, refused, fifthRelay.front.LocalAddr().String(), base+40)
	command.Args[2] = "7"
	command.Args[4] = "1" // End before libcoap's automatic 60-second retry.
	started := time.Now()
	output, err := command.CombinedOutput()
	decodedRefusal := false
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "v:1 ") && strings.Contains(line, " c:5.03 ") {
			decodedRefusal = true
		}
	}
	if err != nil || !decodedRefusal {
		reply := message.Message{Options: make(message.Options, 0, 32)}
		_, decodeErr := coder.DefaultCoder.Decode(fifthRelay.latestReply(), &reply)
		t.Fatalf("native client did not decrypt capacity refusal after %s: %v; outer=%v decoded=%v payload-length=%d", time.Since(started), err, reply.Code, decodeErr, len(reply.Payload))
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 1 || native[0].Message != seed.Message {
		t.Fatal("full response cap reached AIRC intake", native)
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	var jobs int
	err = db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE principal=? AND binding=? AND request_id=?", f.config.Principals[0].ID, f.config.Principals[0].Binding, refused.RequestID).Scan(&jobs)
	closeErr := db.Close()
	if err != nil || closeErr != nil || jobs != 0 {
		t.Fatal("full native response cap reached durable intake", jobs, err, closeErr)
	}
	if delay := time.Until(last.Add(1500 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	for _, relay := range relays {
		relay.continueResponse(t)
		_ = oscoreResponseTime(t, relay)
	}
	// Still-connected clients have unfinished multi-block responses. A continuation
	// cannot renew the age of any retained body; all expire by the original deadline.
	if delay := time.Until(last.Add(3200 * time.Millisecond)); delay > 0 {
		time.Sleep(delay)
	}
	if time.Since(last) > 4*time.Second {
		t.Fatal("fixture missed distinction between absolute and renewed expiration")
	}
	if err := os.WriteFile(f.sequence, []byte(strconv.FormatUint(base+64, 10)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	accepted := oscoreAccepted(t, f, refused)
	if accepted.State != core.Accepted || accepted.SendAttempts != 1 {
		t.Fatal("capacity did not recover after absolute expiration", accepted)
	}
	native = nativeWebTransportMessages(t, f.config.Bindings[0].Address, room.Name)
	if len(native) != 2 || native[1].ID != accepted.MessageID || native[1].From != "fixture-author" || native[1].Message != refused.Message {
		t.Fatal("recovered capacity changed native send", native)
	}
}
