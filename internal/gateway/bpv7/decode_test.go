package bpv7

import (
	"bytes"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"math"
	"strings"
	"testing"
)

func TestCRCReferenceChecks(t *testing.T) {
	data := []byte("123456789")
	if got := crc16X25(data, -1); got != 0x906e || got != referenceCRC16(data) {
		t.Fatalf("X25 CRC=%04x", got)
	}
	if got := crc32.Checksum(data, castagnoli); got != 0xe3069283 {
		t.Fatalf("CRC32C=%08x", got)
	}
}

func TestDecodeProfileFormsAndIdentity(t *testing.T) {
	for _, indefinite := range []bool{false, true} {
		for _, tag := range []bool{false, true} {
			for _, primaryCRC := range []uint64{1, 2} {
				for _, payloadCRC := range []uint64{0, 1, 2} {
					f := defaultWire()
					f.indefinite, f.tag, f.primaryCRC, f.payloadCRC = indefinite, tag, primaryCRC, payloadCRC
					data := f.bytes()
					before := bytes.Clone(data)
					b, err := Decode(data)
					if err != nil || b.Source != f.source || b.Destination != f.destination || b.Payload != f.payload || b.CreationMilliseconds != f.creation || b.Sequence != f.sequence || b.LifetimeMilliseconds != f.lifetime {
						t.Fatalf("decode form indefinite=%v tag=%v crc=%d/%d: %+v %v", indefinite, tag, primaryCRC, payloadCRC, b, err)
					}
					if !bytes.Equal(data, before) {
						t.Fatal("decoder mutated input CRC bytes")
					}
					if b.IssuedAt() != EpochSeconds+1000 || b.ExpiresAt() != EpochSeconds+1060 {
						t.Fatal("DTN epoch/lifetime conversion", b)
					}
					if len(b.RequestID()) != 47 || b.RequestID() != (Bundle{Source: f.source, CreationMilliseconds: f.creation, Sequence: f.sequence}).RequestID() {
						t.Fatal("identity depends on transport representation")
					}
				}
			}
		}
	}
}

func TestDecodeRefusalBoundaries(t *testing.T) {
	mutations := map[string]func(*wireFixture){
		"fragment":                  func(f *wireFixture) { f.flags = 1 },
		"status report":             func(f *wireFixture) { f.flags = 2 },
		"requested report":          func(f *wireFixture) { f.flags = 16384 },
		"primary no CRC":            func(f *wireFixture) { f.primaryCRC = 0 },
		"invalid primary CRC":       func(f *wireFixture) { f.primaryCRC = 3 },
		"source is application":     func(f *wireFixture) { f.source = "dtn://sender/app" },
		"anonymous source":          func(f *wireFixture) { f.source = "dtn:none" },
		"multicast destination":     func(f *wireFixture) { f.destination = "dtn://receiver/~app" },
		"uppercase source":          func(f *wireFixture) { f.source = "dtn://Sender/" },
		"missing destination demux": func(f *wireFixture) { f.destination = "dtn://receiver/" },
		"unknown creation time":     func(f *wireFixture) { f.creation = 0 },
		"zero lifetime":             func(f *wireFixture) { f.lifetime = 0 },
		"creation overflow":         func(f *wireFixture) { f.creation = math.MaxUint64 },
		"lifetime overflow":         func(f *wireFixture) { f.lifetime = math.MaxUint64 },
		"time sum overflow":         func(f *wireFixture) { f.creation = math.MaxInt64 - epochMilliseconds - 1; f.lifetime = 2 },
		"nonpayload block":          func(f *wireFixture) { f.blockType = 7 },
		"wrong block number":        func(f *wireFixture) { f.blockNumber = 2 },
		"payload control flags":     func(f *wireFixture) { f.blockFlags = 1 },
		"invalid payload CRC":       func(f *wireFixture) { f.payloadCRC = 3 },
		"empty payload":             func(f *wireFixture) { f.payload = "" },
		"oversize payload":          func(f *wireFixture) { f.payload = strings.Repeat("x", MaxPayloadBytes+1) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			f := defaultWire()
			mutate(&f)
			if _, err := Decode(f.bytes()); !errors.Is(err, ErrBundle) {
				t.Fatal("accepted invalid profile", err)
			}
		})
	}
	f := defaultWire()
	f.payload = strings.Repeat("x", MaxPayloadBytes)
	if _, err := Decode(f.bytes()); err != nil {
		t.Fatal("exact payload limit refused", err)
	}
}

func TestMalformedWireAndChecksumRefusal(t *testing.T) {
	wire := defaultWire().bytes()
	for length := 0; length < len(wire); length++ {
		if _, err := Decode(wire[:length]); err == nil {
			t.Fatalf("accepted truncated prefix %d", length)
		}
	}
	for index := range wire {
		changed := bytes.Clone(wire)
		changed[index] ^= 0x80
		if _, err := Decode(changed); err == nil {
			t.Fatalf("accepted corrupted byte %d", index)
		}
	}
	for _, data := range [][]byte{append(bytes.Clone(wire), 0), append(bytes.Clone(wire[:len(wire)-1]), 0x80, 0xff), bytes.Repeat([]byte{0x9f}, MaxBytes+1), {0x9f, 0x89, 0x18, 7}, {0x9f, 0x89, 7, 4, 1, 0x82, 1, 0x7b, 255, 255, 255, 255, 255, 255, 255, 255}} {
		if _, err := Decode(data); err == nil {
			t.Fatal("accepted malformed/trailing/nonminimal wire")
		}
	}
	// A fixed independently serialized wire vector fixes array and CRC coverage.
	literal := "9f8907040182016f2f2f72656365697665722f616972638201692f2f73656e6465722f820100821a000f42401119ea6042a2c18501010000566f70617175652e7369676e65642e656e76656c6f7065ff"
	data, err := hex.DecodeString(literal)
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Decode(data)
	if err != nil || bundle.RequestID() != "bp7-3ZcbrV9Lzf3YawyS4vpuxrFnY2T-xk9jaa5Wz_pCtCk" {
		t.Fatal("independent wire/identity vector", bundle, err)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(defaultWire().bytes())
	f.Add([]byte{0x9f, 0xff})
	f.Add([]byte{0x9f, 0x89, 7, 4, 2})
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		b, err := Decode(data)
		if err == nil {
			if len(b.Payload) == 0 || len(b.Payload) > MaxPayloadBytes || !Endpoint(b.Source, true) || !Endpoint(b.Destination, false) || len(data) > MaxBytes {
				t.Fatal("decoder escaped its profile bounds")
			}
		}
	})
}
