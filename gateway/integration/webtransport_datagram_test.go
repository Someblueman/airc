//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/adapter"
)

const (
	webTransportDatagramHeader = 28
	webTransportDatagramChunk  = 972
)

func webTransportDatagrams(body []byte, id [16]byte) [][]byte {
	count := (len(body) + webTransportDatagramChunk - 1) / webTransportDatagramChunk
	fragments := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		start := index * webTransportDatagramChunk
		end := start + webTransportDatagramChunk
		if end > len(body) {
			end = len(body)
		}
		fragment := make([]byte, webTransportDatagramHeader+end-start)
		copy(fragment[:4], "AGD1")
		copy(fragment[4:20], id[:])
		binary.BigEndian.PutUint32(fragment[20:24], uint32(len(body)))
		binary.BigEndian.PutUint16(fragment[24:26], uint16(index))
		binary.BigEndian.PutUint16(fragment[26:28], uint16(count))
		copy(fragment[28:], body[start:end])
		fragments = append(fragments, fragment)
	}
	return fragments
}

func assembleWebTransportDatagrams(t *testing.T, fragments [][]byte) []byte {
	t.Helper()
	if len(fragments) == 0 {
		t.Fatal("empty WebTransport datagram response")
	}
	total := int(binary.BigEndian.Uint32(fragments[0][20:24]))
	count := int(binary.BigEndian.Uint16(fragments[0][26:28]))
	parts := make([][]byte, count)
	for _, fragment := range fragments {
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" || int(binary.BigEndian.Uint32(fragment[20:24])) != total {
			t.Fatalf("invalid response datagram=%x", fragment[:min(len(fragment), webTransportDatagramHeader)])
		}
		index := int(binary.BigEndian.Uint16(fragment[24:26]))
		if index >= count {
			t.Fatalf("response datagram index=%d count=%d", index, count)
		}
		parts[index] = append([]byte(nil), fragment[webTransportDatagramHeader:]...)
	}
	out := make([]byte, 0, total)
	for index, part := range parts {
		if part == nil {
			t.Fatalf("missing response datagram index=%d", index)
		}
		out = append(out, part...)
	}
	if len(out) != total {
		t.Fatalf("response datagram bytes=%d want=%d", len(out), total)
	}
	return out
}

func receiveWebTransportDatagramFrame(t *testing.T, f webTransportFixture, ctx context.Context) adapter.FrameResponse {
	t.Helper()
	fragments := make([][]byte, 0, 32)
	var count int
	for {
		fragment, err := f.session.ReceiveDatagram(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(fragment) < webTransportDatagramHeader {
			t.Fatalf("short response datagram: %d", len(fragment))
		}
		if count == 0 {
			count = int(binary.BigEndian.Uint16(fragment[26:28]))
		}
		fragments = append(fragments, fragment)
		if len(fragments) == count {
			break
		}
	}
	var response adapter.FrameResponse
	if err := jsonUnmarshalFrame(t, assembleWebTransportDatagrams(t, fragments), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func jsonUnmarshalFrame(t *testing.T, data []byte, target any) error {
	t.Helper()
	if len(data) > adapter.MaxResponseFrameBytes {
		return fmt.Errorf("response exceeds bound: %d", len(data))
	}
	return json.Unmarshal(data, target)
}
