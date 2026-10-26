//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/core"
)

func readRawQUICDatagramFrame(t *testing.T, f rawQUICFixture, ctx context.Context) adapter.FrameResponse {
	t.Helper()
	var fragments [][]byte
	count := 0
	for {
		fragment, err := f.conn.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" {
			t.Fatalf("invalid raw QUIC datagram response length=%d", len(fragment))
		}
		if count == 0 {
			count = int(binary.BigEndian.Uint16(fragment[26:28]))
		}
		fragments = append(fragments, fragment)
		if len(fragments) == count {
			break
		}
	}
	body := assembleWebTransportDatagrams(t, fragments)
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func rawQUICDatagramCall(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	frame := rawQUICRequest(t, f, request, encrypted, false)
	frame.CorrelationID = "quic-" + rand.Text()
	body := rawQUICFrame(t, frame)[4:]
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range webTransportDatagrams(body, id) {
		if err := f.conn.SendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	response := readRawQUICDatagramFrame(t, f, ctx)
	if response.Version != 1 || response.CorrelationID != frame.CorrelationID || response.Event != "result" || response.Sequence != 1 || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatalf("raw QUIC datagram response=%+v error=%+v", response, response.Error)
	}
	return response
}
