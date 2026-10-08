package kafkawire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func versionsBody() []byte {
	body := []byte{0, 0, 0, 0, 0, 8}
	for _, v := range [][2]int16{{18, 0}, {17, 1}, {36, 1}, {3, 8}, {2, 5}, {1, 11}, {0, 7}, {23, 3}} {
		body = binary.BigEndian.AppendUint16(body, uint16(v[0]))
		body = binary.BigEndian.AppendUint16(body, 0)
		body = binary.BigEndian.AppendUint16(body, uint16(v[1]))
	}
	return body
}

func TestKafkaVersionNegotiation(t *testing.T) {
	valid := versionsBody()
	if err := VersionsReply(valid); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(valid); n++ {
		if err := VersionsReply(valid[:n]); !errors.Is(err, ErrFrame) {
			t.Fatal("truncation", n)
		}
	}
	for _, mutate := range []func([]byte){
		func(b []byte) { b[1] = 1 }, func(b []byte) { b[5] = 255 }, func(b []byte) { b[6] = 255 }, func(b []byte) { b[11] = 255; b[10] = 255 },
		func(b []byte) { copy(b[12:18], b[6:12]) }, func(b []byte) { b[len(b)-1] = 2 }, func(b []byte) { b[8] = 1 },
	} {
		b := bytes.Clone(valid)
		mutate(b)
		if err := VersionsReply(b); !errors.Is(err, ErrFrame) {
			t.Fatal("unsafe versions accepted", b)
		}
	}
	if err := VersionsReply(append(valid, 0)); !errors.Is(err, ErrFrame) {
		t.Fatal("trailing bytes")
	}
	missing := bytes.Clone(valid[:len(valid)-6])
	missing[5] = 7
	if err := VersionsReply(missing); !errors.Is(err, ErrFrame) {
		t.Fatal("missing API")
	}
	unknown := append(bytes.Clone(valid), 0, 99, 0, 0, 0, 1)
	unknown[5] = 9
	if err := VersionsReply(unknown); err != nil {
		t.Fatal("unknown API", err)
	}
}

func TestKafkaPLAINCommands(t *testing.T) {
	if !bytes.Equal(PlainHandshake(), []byte{0, 5, 'P', 'L', 'A', 'I', 'N'}) {
		t.Fatal("handshake")
	}
	reply := append([]byte{0, 0, 0, 0, 0, 1}, PlainHandshake()...)
	if err := PlainHandshakeReply(reply); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(reply); n++ {
		if err := PlainHandshakeReply(reply[:n]); !errors.Is(err, ErrFrame) {
			t.Fatal("handshake truncation", n)
		}
	}
	for _, bad := range [][]byte{append(bytes.Clone(reply), 0), {0, 1, 0, 0, 0, 1, 0, 5, 'P', 'L', 'A', 'I', 'N'}, {0, 0, 0, 0, 0, 0}, {0, 0, 0, 0, 0, 1, 0, 1, 'X'}} {
		if err := PlainHandshakeReply(bad); !errors.Is(err, ErrFrame) {
			t.Fatal("handshake refusal")
		}
	}
	duplicate := append(bytes.Clone(reply), PlainHandshake()...)
	duplicate[5] = 2
	if err := PlainHandshakeReply(duplicate); !errors.Is(err, ErrFrame) {
		t.Fatal("duplicate mechanism")
	}
	password := []byte("secret")
	body, err := PlainAuthenticate("user", password)
	if err != nil || !bytes.Equal(body, []byte{0, 0, 0, 12, 0, 'u', 's', 'e', 'r', 0, 's', 'e', 'c', 'r', 'e', 't'}) {
		t.Fatal("SASL bytes", body, err)
	}
	password[0] = 'X'
	if body[10] != 's' {
		t.Fatal("credential aliases input")
	}
	clear(body)
	for _, v := range []struct {
		user     string
		password []byte
	}{{"", []byte("p")}, {"u", nil}, {"u\x00", []byte("p")}, {"u", []byte("p\x00")}, {"\xff", []byte("p")}, {"u", []byte{255}}, {strings.Repeat("u", 256), []byte("p")}, {"u", bytes.Repeat([]byte("p"), 4097)}} {
		if body, err := PlainAuthenticate(v.user, v.password); !errors.Is(err, ErrFrame) || body != nil {
			t.Fatal("bad credential")
		}
	}
}

func TestKafkaPLAINAuthenticationReply(t *testing.T) {
	valid := []byte{0, 0, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3, 232}
	if lifetime, err := PlainAuthenticateReply(valid); err != nil || lifetime != 1000 {
		t.Fatal(lifetime, err)
	}
	for n := 0; n < len(valid); n++ {
		if _, err := PlainAuthenticateReply(valid[:n]); !errors.Is(err, ErrFrame) {
			t.Fatal("auth truncation", n)
		}
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[1] = 1 }, func(b []byte) { b[3] = 254 }, func(b []byte) { b[7] = 1 }, func(b []byte) { b[8] = 255 }} {
		b := bytes.Clone(valid)
		mutate(b)
		if lifetime, err := PlainAuthenticateReply(b); !errors.Is(err, ErrFrame) || lifetime != 0 {
			t.Fatal("bad auth response")
		}
	}
	if _, err := PlainAuthenticateReply(append(valid, 0)); !errors.Is(err, ErrFrame) {
		t.Fatal("trailing auth bytes")
	}
}
