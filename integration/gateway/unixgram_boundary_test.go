//go:build darwin || linux

package gateway_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUnixgramOversizeTamperSignedAndIncompleteCannotAdmit(t *testing.T) {
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
	index := len(tampered.Envelope) - 10
	value := byte('A')
	if tampered.Envelope[index] == value {
		value = 'B'
	}
	tampered.Envelope = tampered.Envelope[:index] + string(value) + tampered.Envelope[index+1:]
	writeUnixgramFragments(t, f, unixgramFragments(t, tampered))
	signed := frame
	signed.Profile, signed.Envelope = "signed", f.credentials.token(t, request, false)
	writeUnixgramFragments(t, f, unixgramFragments(t, signed))
	writeUnixgramFragments(t, f, fragments[:1])
	// A valid capability response fences preceding input processing, while
	// admission remains absent for every invalid/incomplete mutation.
	unixgramCall(t, f, core.Request{Version: 1, RequestID: "ug-boundary-fence", Operation: core.CapabilitiesGet}, true)
	rpcNoJobs(t, f.config.StateDir)
	if records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name); len(records) != 0 {
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
	records := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if receipt.SendAttempts != 1 || len(records) != 1 || records[0].ID != receipt.MessageID || records[0].Message != request.Message {
		t.Fatalf("Unix datagram positive native facts receipt=%+v records=%+v", receipt, records)
	}
}
