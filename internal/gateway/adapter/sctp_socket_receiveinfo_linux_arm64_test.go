//go:build linux && arm64

package adapter

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestSCTPReceiveInfoRequiresConfiguredModeStreamZeroAndPPIDZero(t *testing.T) {
	valid := make([]byte, sctpReceiveInfoSize)
	if _, err := parseSCTPReceiveInfoData(valid, SCTPOrdered); err != nil {
		t.Fatalf("ordered metadata rejected: %v", err)
	}
	unordered := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint16(unordered[4:6], sctpMessageUnordered)
	if _, err := parseSCTPReceiveInfoData(unordered, SCTPUnordered); err != nil {
		t.Fatalf("unordered metadata rejected: %v", err)
	}
	if _, err := parseSCTPReceiveInfoData(unordered, SCTPOrdered); !errors.Is(err, errSCTPInvalidRecord) {
		t.Fatalf("unordered metadata accepted in ordered mode: %v", err)
	}
	if _, err := parseSCTPReceiveInfoData(valid, SCTPUnordered); !errors.Is(err, errSCTPInvalidRecord) {
		t.Fatalf("ordered metadata accepted in unordered mode: %v", err)
	}
	for name, mutate := range map[string]func([]byte){
		"nonzero stream": func(data []byte) { binary.LittleEndian.PutUint16(data[:2], 1) },
		"nonzero ppid":   func(data []byte) { binary.BigEndian.PutUint32(data[8:12], 1) },
	} {
		t.Run(name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			mutate(data)
			if _, err := parseSCTPReceiveInfoData(data, SCTPOrdered); !errors.Is(err, errSCTPInvalidRecord) {
				t.Fatalf("metadata error = %v", err)
			}
		})
	}
	if _, err := parseSCTPReceiveInfoData(valid[:len(valid)-1], SCTPOrdered); !errors.Is(err, errSCTPInvalidRecord) {
		t.Fatalf("short metadata error = %v", err)
	}
}

func TestSCTPReceiveInfoTracksIdentityAcrossFragments(t *testing.T) {
	firstData := make([]byte, sctpReceiveInfoSize)
	first, err := parseSCTPReceiveInfoData(firstData, SCTPOrdered)
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func([]byte){
		"stream sequence": func(data []byte) { binary.LittleEndian.PutUint16(data[2:4], 1) },
		"context":         func(data []byte) { binary.LittleEndian.PutUint32(data[20:24], 1) },
		"association":     func(data []byte) { binary.LittleEndian.PutUint32(data[24:28], 1) },
	} {
		t.Run(name, func(t *testing.T) {
			fragmentData := append([]byte(nil), firstData...)
			change(fragmentData)
			fragment, err := parseSCTPReceiveInfoData(fragmentData, SCTPOrdered)
			if err != nil {
				t.Fatal(err)
			}
			if first == fragment {
				t.Fatal("different SCTP message identity accepted as same record")
			}
		})
	}
	fragmentData := append([]byte(nil), firstData...)
	binary.LittleEndian.PutUint32(fragmentData[12:16], 11) // rcv_tsn can advance per data chunk.
	binary.LittleEndian.PutUint32(fragmentData[16:20], 22) // rcv_cumtsn can advance between receives.
	fragment, err := parseSCTPReceiveInfoData(fragmentData, SCTPOrdered)
	if err != nil || fragment != first {
		t.Fatalf("chunk sequence metadata changed record identity: %+v, %v", fragment, err)
	}
}
