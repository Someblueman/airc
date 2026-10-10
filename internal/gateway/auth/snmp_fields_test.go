//go:build unix

package auth

import (
	"encoding/asn1"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/testsnmp"
)

func TestSNMPFieldRendering(t *testing.T) {
	cases := []struct {
		kind        string
		tag         int
		application bool
		data        []byte
		want        string
	}{
		{"hex", 4, false, []byte{0, 255}, "00ff"},
		{"integer", 2, false, []byte{255}, "-1"},
		{"unsigned", 1, true, []byte{0, 255, 255, 255, 255}, "4294967295"},
		{"unsigned", 6, true, []byte{0, 255, 255, 255, 255, 255, 255, 255, 255}, "18446744073709551615"},
		{"oid", 6, false, []byte{43, 6, 1}, "1.3.6.1"},
		{"ip", 0, true, []byte{192, 0, 2, 7}, "192.0.2.7"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+tc.want, func(t *testing.T) {
			v, o, p, k := snmpFixture(t, true)
			o.Routes[0].Notifications[0].Fields[0].Type = tc.kind
			p.Fields[2].Tag = tc.tag
			p.Fields[2].Application = tc.application
			p.Fields[2].Data = tc.data
			a, err := NewSNMPAuthenticator(v, o)
			if err != nil {
				t.Fatal(err)
			}
			n, err := a.Verify(testsnmp.Encode(t, p, k))
			if err != nil || n.Body() != "SNMP Alert\nMessage: "+tc.want {
				t.Fatal(n.Body(), err)
			}
		})
	}
	v, o, p, k := snmpFixture(t, true)
	n := &o.Routes[0].Notifications[0]
	n.Fields[0].Required = false
	n.Fields = append(n.Fields, config.SNMPField{OID: "1.3.6.1.4.1.123.3", Name: "First", Type: "text", Required: true})
	p.Fields = append(p.Fields, testsnmp.Field{OID: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 3}, Tag: 4, Data: []byte("first")})
	p.Fields[2], p.Fields[3] = p.Fields[3], p.Fields[2]
	a, err := NewSNMPAuthenticator(v, o)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := a.Verify(testsnmp.Encode(t, p, k))
	if err != nil || proof.Body() != "SNMP Alert\nMessage: 世界\nbody\nFirst: first" {
		t.Fatal("configuration order", proof.Body(), err)
	}
	p.Fields = p.Fields[:3]
	proof, err = a.Verify(testsnmp.Encode(t, p, k))
	if err != nil || proof.Body() != "SNMP Alert\nFirst: first" {
		t.Fatal("optional omission", proof.Body(), err)
	}
}

func TestSNMPExactMessageBound(t *testing.T) {
	v, o, p, k := snmpFixture(t, true)
	n := &o.Routes[0].Notifications[0]
	n.Fields = nil
	p.Fields = p.Fields[:2]
	// Seven full 512-byte fields plus one shorter field create exactly 4096
	// rendered bytes. Increasing the last value by one must refuse, not truncate.
	remaining := 4096 - len("SNMP Alert")
	for i := 0; i < 8; i++ {
		oid := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 123, 10 + i}
		name := fmt.Sprintf("F%d", i)
		size := 512
		if i == 7 {
			size = remaining - len("\n"+name+": ")
		}
		remaining -= len("\n"+name+": ") + size
		n.Fields = append(n.Fields, config.SNMPField{OID: oid.String(), Name: name, Type: "text", Required: true})
		p.Fields = append(p.Fields, testsnmp.Field{OID: oid, Tag: 4, Data: []byte(strings.Repeat("x", size))})
	}
	if remaining != 0 {
		t.Fatal("fixture arithmetic")
	}
	a, err := NewSNMPAuthenticator(v, o)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := a.Verify(testsnmp.Encode(t, p, k))
	if err != nil || len(proof.Body()) != 4096 {
		t.Fatal("exact bound", len(proof.Body()), err)
	}
	p.Fields[len(p.Fields)-1].Data = append(p.Fields[len(p.Fields)-1].Data, 'x')
	if proof, err := a.Verify(testsnmp.Encode(t, p, k)); err == nil || proof.Valid() {
		t.Fatal("oversize admitted")
	}
}
