package adapter

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
)

func TestWebTransportDatagramsRoundTripOutOfOrder(t *testing.T) {
	want := []byte(strings.Repeat("datagram payload ", 900))
	fragments, err := splitFragments(want)
	if err != nil {
		t.Fatal(err)
	}
	if len(fragments) < 2 {
		t.Fatalf("test payload produced %d fragments", len(fragments))
	}
	for _, fragment := range fragments {
		if len(fragment) > fragmentDatagramBytes {
			t.Fatalf("fragment length=%d", len(fragment))
		}
	}
	assembler := newDatagramAssembler()
	now := time.Unix(100, 0)
	var got []byte
	for i := len(fragments) - 1; i >= 0; i-- {
		got, err = assembler.add(fragments[i], now)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("assembled payload differs: got=%d want=%d", len(got), len(want))
	}
	if len(assembler.pending) != 0 || len(assembler.completed) != 1 {
		t.Fatalf("assembly state pending=%d completed=%d", len(assembler.pending), len(assembler.completed))
	}
	if duplicate, err := assembler.add(fragments[0], now.Add(time.Second)); err != nil || duplicate != nil {
		t.Fatalf("replayed fragment was not suppressed: payload=%d err=%v", len(duplicate), err)
	}
}

func TestWebTransportDatagramsRejectMalformedAndConflictingFragments(t *testing.T) {
	id := [16]byte{1}
	valid := makeWebTransportDatagram(id, fragmentPayloadBytes+3, 0, 2, bytes.Repeat([]byte{'a'}, fragmentPayloadBytes))
	for _, test := range []struct {
		name string
		data []byte
	}{
		{"magic", append([]byte("BAD1"), valid[4:]...)},
		{"short", valid[:fragmentHeaderBytes-1]},
		{"oversized total", makeWebTransportDatagram(id, MaxRequestFrameBytes+1, 0, 1, []byte("abc"))},
		{"wrong count", makeWebTransportDatagram(id, 3, 0, 2, []byte("abc"))},
		{"empty final payload", makeWebTransportDatagram(id, fragmentPayloadBytes+1, 1, 2, nil)},
	} {
		t.Run(test.name, func(t *testing.T) {
			assembler := newDatagramAssembler()
			if got, err := assembler.add(test.data, time.Unix(100, 0)); err != nil || got != nil || len(assembler.pending) != 0 {
				t.Fatalf("malformed datagram accepted: got=%d err=%v pending=%d", len(got), err, len(assembler.pending))
			}
		})
	}

	assembler := newDatagramAssembler()
	if got, err := assembler.add(valid, time.Unix(100, 0)); err != nil || got != nil {
		t.Fatalf("valid first fragment failed: got=%d err=%v", len(got), err)
	}
	conflicting := makeWebTransportDatagram(id, fragmentPayloadBytes+3, 0, 2, bytes.Repeat([]byte{'b'}, fragmentPayloadBytes))
	if got, err := assembler.add(conflicting, time.Unix(101, 0)); err != nil || got != nil || len(assembler.pending) != 0 {
		t.Fatalf("conflicting duplicate was retained: got=%d err=%v pending=%d", len(got), err, len(assembler.pending))
	}
}

func TestWebTransportDatagramsBoundIncompleteAssembliesAndExpire(t *testing.T) {
	assembler := newDatagramAssembler()
	for i := 0; i < fragmentMaxPending+1; i++ {
		var id [16]byte
		id[0] = byte(i + 1)
		fragment := makeWebTransportDatagram(id, fragmentPayloadBytes+1, 0, 2, bytes.Repeat([]byte{'x'}, fragmentPayloadBytes))
		if got, err := assembler.add(fragment, time.Unix(100, 0)); err != nil || got != nil {
			t.Fatalf("incomplete assembly %d returned got=%d err=%v", i, len(got), err)
		}
	}
	if len(assembler.pending) != fragmentMaxPending {
		t.Fatalf("pending assemblies=%d want=%d", len(assembler.pending), fragmentMaxPending)
	}
	assembler.expire(time.Unix(106, 0))
	if len(assembler.pending) != 0 {
		t.Fatalf("expired assemblies remain: %d", len(assembler.pending))
	}
}

func makeWebTransportDatagram(id [16]byte, total, index, count int, payload []byte) []byte {
	data := make([]byte, fragmentHeaderBytes+len(payload))
	copy(data[:4], fragmentMagic)
	copy(data[4:20], id[:])
	binary.BigEndian.PutUint32(data[20:24], uint32(total))
	binary.BigEndian.PutUint16(data[24:26], uint16(index))
	binary.BigEndian.PutUint16(data[26:28], uint16(count))
	copy(data[fragmentHeaderBytes:], payload)
	return data
}
