package snmpwire

import (
	"bytes"
	"testing"

	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestAuthenticatedTrapStructuralRefusals(t *testing.T) {
	base, key, protocol := fixture(t, true)
	verifier, e := NewVerifier(protocol, key)
	if e != nil {
		t.Fatal(e)
	}
	for _, kind := range []string{"missing_standard", "wrong_standard_order", "wrong_uptime_type", "duplicate", "oversize_value", "count", "empty_user", "engine", "zero_engine", "ff_engine", "oversize_context", "negative_boots", "clock_overflow", "no_auth", "privacy", "invalid_security_level"} {
		t.Run(kind, func(t *testing.T) {
			f := base
			f.Fields = append([]testsnmp.Field(nil), base.Fields...)
			switch kind {
			case "missing_standard":
				f.Fields = f.Fields[1:]
			case "wrong_standard_order":
				f.Fields[0], f.Fields[1] = f.Fields[1], f.Fields[0]
			case "wrong_uptime_type":
				f.Fields[0].Application = false
			case "duplicate":
				f.Fields = append(f.Fields, f.Fields[2])
			case "oversize_value":
				f.Fields[2].Data = make([]byte, 513)
			case "count":
				for n := 0; n < 16; n++ {
					field := f.Fields[2]
					field.OID = append(append([]int(nil), field.OID...), n)
					f.Fields = append(f.Fields, field)
				}
			case "empty_user":
				f.User = ""
			case "engine":
				f.Engine = []byte{1, 2, 3, 4}
			case "zero_engine":
				f.Engine = make([]byte, 8)
			case "ff_engine":
				f.Engine = bytes.Repeat([]byte{255}, 8)
			case "oversize_context":
				f.Context = string(bytes.Repeat([]byte{'x'}, 33))
			case "negative_boots":
				f.Boots = -1
			case "no_auth":
				f.Flags = 0
			case "privacy":
				f.Flags = 3
			case "invalid_security_level":
				f.Flags = 2
			case "clock_overflow":
				f.Time = 2147483648
			}
			if _, e := verifier.Verify(testsnmp.Encode(t, f, key)); e == nil {
				t.Fatal("authenticated malformed trap accepted")
			}
		})
	}
	// An OCTET STRING is opaque wire data, not an authorization or text promise.
	base.Fields[2].Data = []byte{255, 0, 128}
	p, e := verifier.Verify(testsnmp.Encode(t, base, key))
	if e != nil || !bytes.Equal(p.Variables[0].Bytes, base.Fields[2].Data) {
		t.Fatal("opaque octets lost", e)
	}
	raw := make([]byte, MaxPacket+1)
	if _, e := Parse(raw); e == nil {
		t.Fatal("oversize datagram accepted")
	}
}
func TestVariableIntegerAndOIDBounds(t *testing.T) {
	for _, test := range []struct {
		tag  byte
		data []byte
		ok   bool
	}{
		{2, []byte{128, 0, 0, 0}, true}, {2, []byte{127, 255, 255, 255}, true},
		{2, []byte{0, 128, 0, 0, 0}, false}, {2, []byte{0, 1}, false},
		{65, []byte{0, 255, 255, 255, 255}, true}, {65, []byte{1, 0, 0, 0, 0}, false},
		{70, []byte{0, 255, 255, 255, 255, 255, 255, 255, 255}, true},
		{70, []byte{1, 0, 0, 0, 0, 0, 0, 0, 0}, false},
		{64, []byte{127, 0, 0, 1}, true}, {64, []byte{127}, false}, {5, nil, false},
	} {
		_, ok := parseVariable("1.3", test.tag, reader{raw: test.data, end: len(test.data)})
		if ok != test.ok {
			t.Fatal(test, ok)
		}
	}
	if text, ok := decodeOID([]byte{0x90, 0x80, 0x80, 0x80, 0x4f}); !ok || text != "2.4294967295" {
		t.Fatal(text, ok)
	}
}

func FuzzParseTrapNeverPanics(f *testing.F) {
	trap, key, _ := fixture(f, true)
	f.Add(testsnmp.Encode(f, trap, key))
	f.Add([]byte{48, 0})
	f.Add([]byte{48, 132, 255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = Parse(raw) })
}

func TestExactPacketAndFieldCeilings(t *testing.T) {
	f, key, protocol := fixture(t, true)
	f.Fields[2].Data = bytes.Repeat([]byte{'x'}, 512)
	for n := 0; n < 15; n++ {
		field := f.Fields[2]
		field.OID = append(append([]int(nil), field.OID...), n)
		f.Fields = append(f.Fields, field)
	}
	verifier, err := NewVerifier(protocol, key)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	for size := 0; size <= 512; size++ {
		f.Fields[len(f.Fields)-1].Data = bytes.Repeat([]byte{'x'}, size)
		raw = testsnmp.Encode(t, f, key)
		if len(raw) == MaxPacket {
			break
		}
	}
	if len(raw) != MaxPacket {
		t.Fatal("fixture did not reach exact packet ceiling", len(raw))
	}
	p, err := verifier.Verify(raw)
	if err != nil || len(p.Variables) != 16 {
		t.Fatal("exact maximum refused", err)
	}
	if _, err := verifier.Verify(append(raw, 0)); err == nil {
		t.Fatal("next byte accepted")
	}
}

func TestTrapIgnoresAuthenticatedReportableAndReservedFlags(t *testing.T) {
	f, key, protocol := fixture(t, true)
	verifier, e := NewVerifier(protocol, key)
	if e != nil {
		t.Fatal(e)
	}
	for _, flags := range []byte{1, 5, 129, 253} {
		f.Flags = flags
		if _, e := verifier.Verify(testsnmp.Encode(t, f, key)); e != nil {
			t.Fatal(flags, e)
		}
	}
}
