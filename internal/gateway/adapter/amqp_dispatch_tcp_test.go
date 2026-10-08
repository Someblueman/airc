//go:build darwin || linux

package adapter

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestAMQPListenerDurableDispatchSignedResponseAndSettlement(t *testing.T) {
	f := newAMQPAdmissionFixture(t)
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", key)
	if err != nil {
		t.Fatal(err)
	}
	bearer, err := os.ReadFile(f.c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	_, dial, token, cancel, done := amqpListenerWithAdmission(t, slots, 1, f, sealer, strings.TrimSpace(string(bearer)))
	client := dial()
	amqpClientNegotiate(t, client, token)
	for _, p := range []amqpwire.Performative{amqpAttach(true, 1, "request"), amqpAttach(false, 0, "response")} {
		amqpClientWrite(t, client, p)
		if reply := amqpClientRead(t, client); reply.Kind != 0x12 {
			t.Fatal(reply)
		}
	}
	if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 1 {
		t.Fatal(flow)
	}
	nextIncoming, nextOutgoing := uint32(1), uint32(7)
	responseCount := uint32(0)
	request := amqpRequest()
	original := f.incoming(t, request, 0).encoded
	// Replay the same signed ID on a different wire delivery. Settlement, not
	// session acknowledgement alone, restores exactly one request credit.
	for attempt := 0; attempt < 3; attempt++ {
		encoded := original
		outcome := uint64(0x24)
		if attempt == 2 {
			request.Message = "conflicting intent"
			outcome = 0x25
			encoded = f.incoming(t, request, 0).encoded
		}
		first := true
		for len(encoded) > 0 {
			n := min(300, len(encoded))
			p := amqpwire.Performative{Kind: 0x14, Fields: []amqpwire.Value{{Code: 0x70, Number: 1}, {Code: 0x40}, {Code: 0x40}, {Code: 0x40}, {Code: 0x56}, {Code: 0x56}}, Payload: encoded[:n]}
			if first {
				p.Fields[1] = amqpwire.Value{Code: 0x70, Number: uint64(70 + attempt)}
				p.Fields[2] = amqpwire.Value{Code: 0xa0, Data: []byte{byte(attempt)}}
				p.Fields[3] = amqpwire.Value{Code: 0x70}
			}
			if n < len(encoded) {
				p.Fields[5].Number = 1
			}
			amqpClientWrite(t, client, p)
			nextOutgoing++
			encoded = encoded[n:]
			first = false
		}
		disposition := amqpClientRead(t, client)
		if disposition.Kind != 0x15 || disposition.Fields[1].Number != uint64(70+attempt) || disposition.Fields[3].Number != 1 || disposition.Fields[4].Descriptor.Number != outcome {
			t.Fatal(disposition)
		}
		job, err := f.s.Lookup(t.Context(), f.identity, request.RequestID)
		if err != nil || !job.Result.Durable || job.Result.SendAttempts != 0 {
			t.Fatal(job, err)
		}
		if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 0 {
			t.Fatal(flow)
		}
		// Supply output capacity after intake, proving the bounded queued response.
		flow := func() amqpwire.Performative {
			return amqpwire.Performative{Kind: 0x13, Fields: []amqpwire.Value{{Code: 0x70, Number: uint64(nextIncoming)}, {Code: 0x70, Number: 8}, {Code: 0x70, Number: uint64(nextOutgoing)}, {Code: 0x70, Number: 8}, {Code: 0x70}, {Code: 0x70, Number: uint64(responseCount)}, {Code: 0x70, Number: 1}}}
		}
		amqpClientWrite(t, client, flow())
		var response []byte
		var responseID uint32
		frames := 0
		for {
			p := amqpClientRead(t, client)
			if p.Kind != 0x14 {
				t.Fatal(p)
			}
			if frames == 0 {
				responseID = uint32(p.Fields[1].Number)
			}
			response = append(response, p.Payload...)
			frames++
			nextIncoming++
			if amqpField(p, 5).Number == 0 {
				break
			}
			if frames%8 == 0 {
				amqpClientWrite(t, client, flow())
			}
		}
		responseCount++
		message, err := amqpwire.DecodeMessage(response)
		if err != nil {
			t.Fatal(err)
		}
		plain, err := auth.OpenSignedResponse(string(message.Body), "response", pub, amqpResponseScope(f.c.Realm, f.identity.ID(), f.identity.Binding(), f.route()), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		var result AMQPResult
		if err := json.Unmarshal(plain, &result); err != nil {
			t.Fatal(err)
		}
		if result.RequestID != request.RequestID || attempt < 2 && (result.Result == nil || !result.Result.Result.Durable) || attempt == 2 && (result.Result != nil || result.Error == nil || result.Error.Code != core.IDConflict) {
			t.Fatal(result)
		}
		// A received state without settlement does not release retained result bytes.
		amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x15, Fields: []amqpwire.Value{{Code: 0x56, Number: 1}, {Code: 0x70, Number: uint64(responseID)}}})
		fence := flow()
		fence.Fields = append(fence.Fields, amqpwire.Value{Code: 0x40}, amqpwire.Value{Code: 0x56}, amqpwire.Value{Code: 0x56, Number: 1})
		amqpClientWrite(t, client, fence)
		if echo := amqpClientRead(t, client); echo.Kind != 0x13 || echo.Fields[4].Number != 1 {
			t.Fatal("unsettled result released request credit", echo)
		}
		amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x15, Fields: []amqpwire.Value{{Code: 0x56, Number: 1}, {Code: 0x70, Number: uint64(responseID)}, {Code: 0x40}, {Code: 0x56, Number: 1}, amqpOutcome(0x24, "")}})
		if flow := amqpClientRead(t, client); flow.Kind != 0x13 || flow.Fields[6].Number != 1 {
			t.Fatal(flow)
		}
	}
	amqpClientWrite(t, client, amqpwire.Performative{Kind: 0x18})
	if p := amqpClientRead(t, client); p.Kind != 0x18 {
		t.Fatal(p)
	}
	waitAMQPSlots(t, slots, 0)
	cancel()
	if err := amqpListenerFinished(t, done); err != nil {
		t.Fatal(err)
	}
}
