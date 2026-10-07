// Package testsnmp independently encodes fixture SNMPv3 USM trap datagrams.
package testsnmp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/asn1"
	"hash"
	"testing"
)

type Field struct {
	OID         asn1.ObjectIdentifier
	Tag         int
	Data        []byte
	Application bool
}
type Trap struct {
	Engine                            []byte
	User, Context                     string
	Boots, Time, MessageID, RequestID int
	Fields                            []Field
	SHA256                            bool
	Flags                             byte
}

func Encode(t testing.TB, trap Trap, key []byte) []byte {
	t.Helper()
	marshal := func(v any) []byte {
		data, e := asn1.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return data
	}
	seq := func(data []byte) []byte { return marshal(asn1.RawValue{Tag: 16, IsCompound: true, Bytes: data}) }
	var bindings []byte
	for _, f := range trap.Fields {
		class := 0
		if f.Application {
			class = 1
		}
		value := marshal(asn1.RawValue{Class: class, Tag: f.Tag, Bytes: f.Data})
		bindings = append(bindings, seq(append(marshal(f.OID), value...))...)
	}
	pdu := append(marshal(trap.RequestID), marshal(0)...)
	pdu = append(pdu, marshal(0)...)
	pdu = append(pdu, seq(bindings)...)
	scoped := append(marshal(trap.Engine), marshal([]byte(trap.Context))...)
	scoped = append(scoped, marshal(asn1.RawValue{Class: 2, Tag: 7, IsCompound: true, Bytes: pdu})...)
	size := 12
	digest := sha1.New
	if trap.SHA256 {
		size = 24
		digest = sha256.New
	}
	// Use a unique nonzero placeholder to find authentication bytes independently.
	marker := bytes.Repeat([]byte{0xB7}, size)
	security := append(marshal(trap.Engine), marshal(trap.Boots)...)
	security = append(security, marshal(trap.Time)...)
	security = append(security, marshal([]byte(trap.User))...)
	security = append(security, marshal(marker)...)
	security = append(security, marshal([]byte{})...)
	header := append(marshal(trap.MessageID), marshal(65507)...)
	header = append(header, marshal([]byte{trap.Flags})...)
	header = append(header, marshal(3)...)
	packet := append(marshal(3), seq(header)...)
	packet = append(packet, marshal(seq(security))...)
	packet = seq(append(packet, seq(scoped)...))
	offset := bytes.Index(packet, marker)
	if offset < 0 || bytes.Count(packet, marker) != 1 {
		t.Fatal("ambiguous test MAC marker")
	}
	clear(packet[offset : offset+size])
	mac := hmac.New(digest, key)
	mac.Write(packet)
	copy(packet[offset:offset+size], mac.Sum(nil)[:size])
	return packet
}

func LocalizedKey(password string, engine []byte, sha256Profile bool) []byte {
	var digest func() hash.Hash = sha1.New
	if sha256Profile {
		digest = sha256.New
	}
	h := digest()
	chunk := make([]byte, 64)
	for offset := 0; offset < 1048576; offset += len(chunk) {
		for i := range chunk {
			chunk[i] = password[(offset+i)%len(password)]
		}
		h.Write(chunk)
	}
	ku := h.Sum(nil)
	h = digest()
	h.Write(ku)
	h.Write(engine)
	h.Write(ku)
	return h.Sum(nil)
}
