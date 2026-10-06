package bpv7

import (
	"encoding/hex"
	"math"
	"testing"
)

func TestCBORUnsignedEncodingBoundaries(t *testing.T) {
	valid := map[string]uint64{"00": 0, "17": 23, "1818": 24, "18ff": 255, "190100": 256, "19ffff": 65535, "1a00010000": 65536, "1affffffff": 4294967295, "1b0000000100000000": 4294967296, "1bffffffffffffffff": math.MaxUint64}
	for wire, want := range valid {
		data, err := hex.DecodeString(wire)
		if err != nil {
			t.Fatal(err)
		}
		r := reader{data: data}
		if got := r.head(0); r.err != nil || got != want || r.pos != len(data) {
			t.Fatalf("%s: got=%d err=%v", wire, got, r.err)
		}
	}
	for _, wire := range []string{"1800", "1817", "190001", "1900ff", "1a00000001", "1a0000ffff", "1b0000000000000001", "1b00000000ffffffff", "1c", "1d", "1e", "1f", "20", "f6", "1bffff"} {
		data, err := hex.DecodeString(wire)
		if err != nil {
			t.Fatal(err)
		}
		r := reader{data: data}
		r.head(0)
		if r.err == nil {
			t.Fatal("noncanonical/reserved/wrong type accepted", wire)
		}
	}
}
