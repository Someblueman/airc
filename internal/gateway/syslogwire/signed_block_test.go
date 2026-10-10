package syslogwire

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const blockHeader = `<110>1 2026-10-07T00:00:00Z host.example syslogd 123 audit `

func signatureBlockText() string {
	return `[ssign VER="0121" RSID="1" SG="0" SPRI="0" GBC="0" FMN="1" CNT="1" HB="` + base64.StdEncoding.EncodeToString(make([]byte, 32)) + `" SIGN="AAEBAAEB"]`
}

func TestSignedBlockExactBytesAndOwnership(t *testing.T) {
	for _, suffix := range []string{"", " ", " payload\n雪"} {
		text := blockHeader + `[extra text="bracket\] quote\" slash\\"]` + signatureBlockText() + `[tail value="exact"]` + suffix
		raw := []byte(text)
		b, err := ParseSignedBlock(raw)
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Replace(text, ` SIGN="AAEBAAEB"`, "", 1)
		if string(b.Signed) != want || b.Certificate || b.Reboot != 1 || b.First != 1 || len(b.Hashes) != 1 {
			t.Fatalf("incorrect block: %+v", b)
		}
		clear(raw)
		if string(b.Record.Raw) != text || string(b.Signed) != want {
			t.Fatal("caller mutation altered owned bytes")
		}
	}
}

func TestSignedBlockRejectsMalformedFields(t *testing.T) {
	valid := signatureBlockText()
	for _, change := range [][2]string{
		{`VER="0121"`, `VER="0111"`}, {`RSID="1"`, `RSID="01"`},
		{`RSID="1"`, `RSID="10000000000"`}, {`SG="0"`, `SG="4"`},
		{`SPRI="0"`, `SPRI="192"`}, {`GBC="0"`, `GBC="-1"`},
		{`FMN="1"`, `FMN="0"`}, {`CNT="1"`, `CNT="0"`},
		{`CNT="1"`, `CNT="2"`}, {`CNT="1"`, `CNT="100"`},
		{`SIGN="AAEBAAEB"`, `SIGN="AAEBAAEB" EXTRA="x"`},
		{`SIGN="AAEBAAEB"`, `SIGN="AAEB\xAAEB"`},
		{`SG="0" SPRI="0"`, `SPRI="0" SG="0"`},
	} {
		if _, err := ParseSignedBlock([]byte(blockHeader + strings.Replace(valid, change[0], change[1], 1))); err == nil {
			t.Fatalf("accepted %q", change)
		}
	}
	for _, sd := range []string{"-", `[other SIGN="AAEBAAEB"]`, valid + valid, valid + `[ssign-cert]`} {
		if _, err := ParseSignedBlock([]byte(blockHeader + sd)); err == nil {
			t.Fatalf("accepted %q", sd)
		}
	}
}

func TestCertificateBlockFragmentBounds(t *testing.T) {
	text := blockHeader + `[ssign-cert VER="0121" RSID="2" SG="0" SPRI="0" TPBL="9" INDEX="3" FLEN="3" FRAG="abc" SIGN="AAEBAAEB"]`
	b, err := ParseSignedBlock([]byte(text))
	if err != nil || !b.Certificate || b.Total != 9 || b.Index != 3 || !bytes.Equal(b.Fragment, []byte("abc")) {
		t.Fatalf("%+v %v", b, err)
	}
	for _, change := range [][2]string{{`TPBL="9"`, `TPBL="32769"`}, {`INDEX="3"`, `INDEX="0"`}, {`INDEX="3"`, `INDEX="8"`}, {`FLEN="3"`, `FLEN="2"`}, {`FLEN="3"`, `FLEN="0"`}} {
		if _, err := ParseSignedBlock([]byte(strings.Replace(text, change[0], change[1], 1))); err == nil {
			t.Fatalf("accepted %q", change)
		}
	}
}

func TestSignedGroupZeroAllowsPriority(t *testing.T) {
	for _, text := range []string{
		blockHeader + strings.Replace(signatureBlockText(), `SPRI="0"`, `SPRI="110"`, 1),
		blockHeader + `[ssign-cert VER="0121" RSID="1" SG="0" SPRI="110" TPBL="3" INDEX="1" FLEN="3" FRAG="abc" SIGN="AAEBAAEB"]`,
	} {
		b, err := ParseSignedBlock([]byte(text))
		if err != nil || b.Group != 0 || b.Priority != 110 {
			t.Fatalf("%+v %v", b, err)
		}
	}
}
