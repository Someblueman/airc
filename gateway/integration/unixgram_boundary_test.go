//go:build darwin || linux

package gateway_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestUnixgramOversizeTamperUnknownProfileAndIncompleteCannotAdmit(t *testing.T) {
	f := newUnixgramFixture(t)
	request := core.Request{Version: 1, RequestID: "ug-boundary-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: strings.Repeat("b", 3500)}
	frame := unixgramFrame(t, f, request, false)
	fragments := unixgramFragments(t, frame)
	if len(fragments) < 2 || len(fragments[0]) != 1000 {
		t.Fatal("boundary witness must include full valid 1000-byte packet")
	}
	oversized := append(append([]byte(nil), fragments[0]...), byte('x'))
	writeUnixgramFragments(t, f, [][]byte{oversized})
	tampered := frame
	index := len(tampered.Request) - 10
	value := byte('A')
	if tampered.Request[index] == value {
		value = 'B'
	}
	tampered.Request = tampered.Request[:index] + string(value) + tampered.Request[index+1:]
	writeUnixgramFragments(t, f, unixgramFragments(t, tampered))
	// A frame whose request does not validate is refused with a correlated
	// error; a frame with an unknown profile is dropped. Nothing is admitted.
	if response := readUnixgramResponse(t, f, time.Now().Add(5*time.Second)); response.CorrelationID != tampered.CorrelationID || response.Error == nil || response.Error.Code != core.InvalidRequest || response.Result != nil {
		t.Fatalf("tampered Unix datagram response=%+v", response)
	}
	unknown := frame
	unknown.CorrelationID = "ug-unknown-profile"
	unknown.Profile, unknown.Request = "unknown", f.credentials.token(t, request, false)
	writeUnixgramFragments(t, f, unixgramFragments(t, unknown))
	writeUnixgramFragments(t, f, fragments[:1])
	// A valid capability response fences preceding input processing, while
	// admission remains absent for every invalid/incomplete mutation.
	unixgramCall(t, f, core.Request{Version: 1, RequestID: "ug-boundary-fence", Operation: core.CapabilitiesGet}, true)
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name); len(records) != 0 {
		t.Fatalf("invalid Unix datagrams posted: %+v", records)
	}
	// A fresh complete assembly of exactly the same protected intent is the
	// matched positive control, including a full 1000-byte first packet.
	frame.CorrelationID = "ug-boundary-positive"
	writeUnixgramFragments(t, f, unixgramFragments(t, frame))
	response := readUnixgramResponse(t, f, time.Now().Add(5*time.Second))
	if response.Event != "result" || response.CorrelationID != frame.CorrelationID || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
		t.Fatalf("Unix datagram positive control=%+v error=%+v", response, response.Error)
	}
	receipt := pollUnixgramReceipt(t, f, request.RequestID)
	records := nativeWebTransportMessages(t, f.config.Upstream.Address, request.Target.Name)
	if receipt.SendAttempts != 1 || len(records) != 1 || records[0].ID != receipt.MessageID || records[0].Message != request.Message {
		t.Fatalf("Unix datagram positive native facts receipt=%+v records=%+v", receipt, records)
	}
}
