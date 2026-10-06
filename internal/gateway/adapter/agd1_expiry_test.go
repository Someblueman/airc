package adapter

import (
	"bytes"
	"testing"
	"time"
)

func TestAGD1AssemblyExpiryBoundaryAndDuplicateDeadline(t *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		t.Run(offset.String(), func(t *testing.T) {
			a := newDatagramAssembler()
			id := [16]byte{1}
			first := makeWebTransportDatagram(id, agd1PayloadBytes+1, 0, 2, bytes.Repeat([]byte{'x'}, agd1PayloadBytes))
			last := makeWebTransportDatagram(id, agd1PayloadBytes+1, 1, 2, []byte{'y'})
			start := time.Unix(100, 0)
			if got, err := a.add(first, start); err != nil || got != nil {
				t.Fatalf("first fragment: payload=%d err=%v", len(got), err)
			}
			original := a.pending[id]
			if got, err := a.add(first, start.Add(agd1AssemblyAge-time.Second)); err != nil || got != nil {
				t.Fatalf("duplicate: payload=%d err=%v", len(got), err)
			}
			if !original.deadline.Equal(start.Add(agd1AssemblyAge)) || original.received != 1 {
				t.Fatalf("duplicate changed assembly: %+v", original)
			}
			got, err := a.add(last, start.Add(agd1AssemblyAge+offset))
			if err != nil {
				t.Fatal(err)
			}
			if offset < 0 {
				want := append(bytes.Repeat([]byte{'x'}, agd1PayloadBytes), 'y')
				if !bytes.Equal(got, want) || len(a.pending) != 0 || len(a.completed) != 1 {
					t.Fatalf("live completion: payload=%d pending=%d completed=%d", len(got), len(a.pending), len(a.completed))
				}
			} else {
				if got != nil || a.pending[id] == original || a.pending[id].received != 1 || len(a.completed) != 0 {
					t.Fatalf("late fragment reused expired assembly: payload=%d pending=%+v", len(got), a.pending[id])
				}
			}
		})
	}
}
