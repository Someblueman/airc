//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func (p *amqpPeer) submit(t *testing.T, token string) uint32 {
	t.Helper()
	return p.submitMessage(t, aqDescribed(0x75, aqBytes(0xb0, []byte(token))))
}

func (p *amqpPeer) submitMessage(t *testing.T, encoded []byte) uint32 {
	t.Helper()
	id := p.delivery
	p.delivery++
	first := true
	frames := 0
	for len(encoded) > 0 {
		n := min(300, len(encoded))
		more := byte(0x42)
		if n < len(encoded) {
			more = 0x41
		}
		fields := [][]byte{aqUint(1), {0x40}, {0x40}, {0x40}, {0x42}, {more}}
		if first {
			fields[1] = aqUint(id)
			fields[2] = aqBytes(0xb0, []byte{byte(id)})
			fields[3] = aqUint(0)
		}
		aqWrite(t, p.conn, aqFrame(0x14, 0, fields, encoded[:n]))
		p.nextOutgoing++
		frames++
		encoded = encoded[n:]
		first = false
		if frames%8 == 0 && len(encoded) > 0 {
			if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 0 {
				t.Fatal(flow)
			}
		}
	}
	return id
}
func (p *amqpPeer) result(t *testing.T, f *amqpFixture, requestID string, delivery uint32, expectedOutcome uint64) adapter.AMQPResult {
	t.Helper()
	disposition := aqRead(t, p.conn)
	if disposition.kind != 0x15 || aqField(disposition, 0).number != 1 || aqField(disposition, 1).number != uint64(delivery) || aqField(disposition, 3).number != 1 || aqField(disposition, 4).descriptor != expectedOutcome {
		t.Fatal(disposition)
	}
	if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 0 {
		t.Fatal(flow)
	}
	var encoded []byte
	var responseID uint32
	frames := 0
	for {
		transfer := aqRead(t, p.conn)
		if transfer.kind != 0x14 || aqField(transfer, 0).number != 1 || aqField(transfer, 4).number != 0 {
			t.Fatal(transfer)
		}
		if frames == 0 {
			responseID = uint32(aqField(transfer, 1).number)
		}
		encoded = append(encoded, transfer.payload...)
		frames++
		p.nextIncoming++
		if len(encoded) > 65536 {
			t.Fatal("unbounded AMQP result")
		}
		if aqField(transfer, 5).number == 0 {
			break
		}
		if frames%8 == 0 {
			p.flow(t)
		}
	}
	message := aqDecode(t, bytes.NewReader(encoded), 0)
	if message.descriptor != 0x75 || len(message.data) == 0 {
		t.Fatal("AMQP result data section", message)
	}
	sum := sha256.Sum256([]byte("amqp1-response\x00request\x00response"))
	scope := auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: hex.EncodeToString(sum[:])}
	plain, err := auth.OpenSignedResponse(string(message.data), "udp-response", f.responseSigning, scope, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var result adapter.AMQPResult
	if err := json.Unmarshal(plain, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || result.Protocol != "amqp/1" || result.RequestID != requestID || result.RequestAddress != "request" || result.ResponseAddress != "response" || (result.Result == nil) == (result.Error == nil) {
		t.Fatal(result)
	}
	p.responseCount++
	aqWrite(t, p.conn, aqFrame(0x15, 0, [][]byte{{0x41}, aqUint(responseID), {0x40}, {0x41}, aqDescribed(0x24, aqList())}, nil))
	if flow := aqRead(t, p.conn); flow.kind != 0x13 || aqField(flow, 6).number != 1 {
		t.Fatal(flow)
	}
	return result
}
func (p *amqpPeer) call(t *testing.T, f *amqpFixture, r core.Request) adapter.AMQPResult {
	t.Helper()
	p.flow(t)
	id := p.submit(t, f.credentials.token(t, r, false))
	return p.result(t, f, r.RequestID, id, 0x24)
}

// Native observation proves posting; this separate bounded fence proves that
// its receipt is committed before a deliberate gateway shutdown.
func (p *amqpPeer) accepted(t *testing.T, f *amqpFixture, request core.Request, messageID string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		response := p.call(t, f, request)
		if response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable {
			t.Fatal(response)
		}
		result := response.Result.Result
		if result.State == core.Accepted {
			if result.MessageID != messageID || result.SendAttempts != 1 {
				t.Fatal(result)
			}
			return
		}
		if !time.Now().Before(deadline) {
			t.Fatal("native receipt did not commit", result)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
