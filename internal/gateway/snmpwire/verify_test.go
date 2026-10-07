package snmpwire

import (
	"bytes"
	"encoding/asn1"
	"testing"

	"github.com/Someblueman/airc/internal/testsnmp"
)

func fixture(t testing.TB, sha256Profile bool) (testsnmp.Trap, []byte, string) {
	t.Helper()
	engine := []byte{128, 0, 0, 1, 2, 3, 4, 5}
	key := testsnmp.LocalizedKey("test-only-fixture-password", engine, sha256Profile)
	f := testsnmp.Trap{Flags: 1, Engine: engine, User: "fixture", Boots: 2, Time: 1234, MessageID: 42, RequestID: -7, SHA256: sha256Profile, Fields: []testsnmp.Field{
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 2, 1, 1, 3, 0}, Tag: 3, Application: true, Data: []byte{123}},
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 6, 3, 1, 1, 4, 1, 0}, Tag: 6, Data: []byte{43, 6, 1, 4, 1, 123, 1}},
		{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 2}, Tag: 4, Data: []byte("exact 世界\nbody")},
	}}
	protocol := HMACSHA196
	if sha256Profile {
		protocol = HMACSHA256192
	}
	return f, key, protocol
}
func TestExactUSMAuthenticationAndOwnedBytes(t *testing.T) {
	for _, sha256Profile := range []bool{false, true} {
		trap, key, protocol := fixture(t, sha256Profile)
		raw := testsnmp.Encode(t, trap, key)
		v, e := NewVerifier(protocol, key)
		if e != nil {
			t.Fatal(e)
		}
		key[0] ^= 255
		p, e := v.Verify(raw)
		if e != nil || p.User != "fixture" || p.Boots != 2 || p.Time != 1234 || p.RequestID != -7 || p.Notification != "1.3.6.1.4.1.123.1" || p.Uptime != 123 || string(p.Variables[0].Bytes) != "exact 世界\nbody" {
			t.Fatal(p, e)
		}
		original := bytes.Clone(raw)
		raw[len(raw)-1] ^= 1
		if !bytes.Equal(p.Raw, original) {
			t.Fatal("caller changed owned bytes")
		}
		if _, e = v.Verify(raw); e == nil {
			t.Fatal("changed covered body authenticated")
		}
		for i := range original {
			changed := bytes.Clone(original)
			changed[i] ^= 1
			if _, e = v.Verify(changed); e == nil {
				t.Fatal("changed byte authenticated", i)
			}
		}
		if _, e = v.Verify(append(original, 0)); e == nil {
			t.Fatal("trailing datagram bytes accepted")
		}
	}
	for _, v := range []*Verifier{nil, {}} {
		if _, e := v.Verify([]byte{48, 0}); e == nil {
			t.Fatal("uninitialized verifier accepted")
		}
	}
	if _, e := NewVerifier("md5", make([]byte, 16)); e == nil {
		t.Fatal("unsupported protocol accepted")
	}
	if _, e := NewVerifier(HMACSHA256192, make([]byte, 20)); e == nil {
		t.Fatal("wrong key length accepted")
	}
}
func TestBERBoundariesAndLegalLongLengths(t *testing.T) {
	for _, raw := range [][]byte{{48, 128}, {48, 132, 255, 255, 255, 255}, {48, 131, 0, 32, 1}, {48, 2, 2}, {31, 0}} {
		if _, e := Parse(raw); e == nil {
			t.Fatal("malformed packet accepted", raw)
		}
	}
	r := reader{raw: []byte{4, 132, 0, 0, 0, 1, 'x'}, end: 7}
	value, ok := r.octets(1)
	if !ok || string(value) != "x" || !r.done() {
		t.Fatal("legal nonminimal definite length refused")
	}
	for _, text := range []string{"1.3.6.1", "2.4294967295.0"} {
		if !OIDText(text) {
			t.Fatal(text)
		}
	}
	for _, text := range []string{".1.3", "1.40", "3.1", "1.03", "1.3.4294967296", "1.+3", "1.3."} {
		if OIDText(text) {
			t.Fatal(text)
		}
	}
	for _, p := range [][]byte{nil, {128, 0}, {129}, {255, 255, 255, 255, 255, 127}} {
		if _, ok := decodeOID(p); ok {
			t.Fatal("malformed OID accepted", p)
		}
	}
}
