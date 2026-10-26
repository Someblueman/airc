//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
)

type datagramResponseReader interface {
	Read([]byte) (int, error)
	SetReadDeadline(time.Time) error
}

func readAGD1Payload(t *testing.T, conn datagramResponseReader, readDeadline time.Time) []byte {
	t.Helper()

	var received [][]byte
	byIndex := make(map[int][]byte)
	var responseID [16]byte
	var responseTotal int
	count := 0
	for {
		if err := conn.SetReadDeadline(readDeadline); err != nil {
			t.Fatal(err)
		}
		fragment := make([]byte, 1001)
		n, err := conn.Read(fragment)
		if err != nil {
			t.Fatal(err)
		}
		fragment = fragment[:n]
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" {
			t.Fatalf("invalid AGD1 response datagram length=%d", len(fragment))
		}
		if count == 0 {
			count = int(binary.BigEndian.Uint16(fragment[26:28]))
			responseTotal = int(binary.BigEndian.Uint32(fragment[20:24]))
			copy(responseID[:], fragment[4:20])
			if count < 1 || count > (adapter.MaxResponseFrameBytes+webTransportDatagramChunk-1)/webTransportDatagramChunk {
				t.Fatalf("invalid AGD1 response datagram count=%d", count)
			}
		}
		index := int(binary.BigEndian.Uint16(fragment[24:26]))
		if int(binary.BigEndian.Uint16(fragment[26:28])) != count || int(binary.BigEndian.Uint32(fragment[20:24])) != responseTotal || !bytes.Equal(fragment[4:20], responseID[:]) || index >= count {
			t.Fatalf("inconsistent AGD1 response fragment")
		}
		if prior, exists := byIndex[index]; exists {
			if !bytes.Equal(prior, fragment) {
				t.Fatalf("conflicting duplicate AGD1 response index=%d", index)
			}
			continue
		}
		byIndex[index] = append([]byte(nil), fragment...)
		if len(byIndex) == count {
			received = make([][]byte, 0, count)
			for _, fragment := range byIndex {
				received = append(received, fragment)
			}
			break
		}
	}
	return assembleResponseDatagrams(t, received)
}
func assembleResponseDatagrams(t *testing.T, fragments [][]byte) []byte {
	t.Helper()
	if len(fragments) == 0 {
		t.Fatal("empty AGD1 response")
	}
	var id [16]byte
	copy(id[:], fragments[0][4:20])
	total := int(binary.BigEndian.Uint32(fragments[0][20:24]))
	count := int(binary.BigEndian.Uint16(fragments[0][26:28]))
	if total < 1 || total > adapter.MaxResponseFrameBytes || count < 1 || count > (adapter.MaxResponseFrameBytes+webTransportDatagramChunk-1)/webTransportDatagramChunk || len(fragments) != count {
		t.Fatalf("invalid AGD1 response total=%d count=%d fragments=%d", total, count, len(fragments))
	}
	if count != (total+webTransportDatagramChunk-1)/webTransportDatagramChunk {
		t.Fatalf("noncanonical AGD1 response count=%d total=%d", count, total)
	}
	parts := make([][]byte, count)
	for _, fragment := range fragments {
		if len(fragment) < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" || string(fragment[4:20]) != string(id[:]) || int(binary.BigEndian.Uint32(fragment[20:24])) != total || int(binary.BigEndian.Uint16(fragment[26:28])) != count {
			t.Fatalf("inconsistent AGD1 response fragment")
		}
		index := int(binary.BigEndian.Uint16(fragment[24:26]))
		if index >= count || parts[index] != nil {
			t.Fatalf("duplicate or invalid AGD1 response index=%d", index)
		}
		payload := fragment[webTransportDatagramHeader:]
		want := webTransportDatagramChunk
		if index == count-1 {
			want = total - webTransportDatagramChunk*(count-1)
		}
		if want < 1 || len(payload) != want {
			t.Fatalf("AGD1 response index=%d bytes=%d want=%d", index, len(payload), want)
		}
		parts[index] = append([]byte(nil), payload...)
	}
	data := make([]byte, 0, total)
	for index, part := range parts {
		if part == nil {
			t.Fatalf("missing AGD1 response index=%d", index)
		}
		data = append(data, part...)
	}
	if len(data) != total {
		t.Fatalf("AGD1 response bytes=%d want=%d", len(data), total)
	}
	return data
}
