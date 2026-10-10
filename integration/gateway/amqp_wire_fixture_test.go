//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// Independent selected AMQP wire client: no production frame/value/message codec.
func aqUint(n uint32) []byte { return binary.BigEndian.AppendUint32([]byte{0x70}, n) }
func aqBytes(code byte, s []byte) []byte {
	return append(binary.BigEndian.AppendUint32([]byte{code}, uint32(len(s))), s...)
}
func aqList(fields ...[]byte) []byte {
	body := []byte{}
	for _, field := range fields {
		body = append(body, field...)
	}
	out := binary.BigEndian.AppendUint32([]byte{0xd0}, uint32(len(body)+4))
	return append(binary.BigEndian.AppendUint32(out, uint32(len(fields))), body...)
}
func aqDescribed(code byte, value []byte) []byte { return append([]byte{0, 0x53, code}, value...) }
func aqFrame(kind, typ byte, fields [][]byte, payload []byte) []byte {
	body := append(aqDescribed(kind, aqList(fields...)), payload...)
	header := binary.BigEndian.AppendUint32(nil, uint32(len(body)+8))
	return append(append(header, 2, typ, 0, 0), body...)
}
func aqWrite(t *testing.T, conn net.Conn, packet []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for len(packet) > 0 {
		n, err := conn.Write(packet)
		if err != nil || n == 0 {
			t.Fatal(n, err)
		}
		packet = packet[n:]
	}
}

type aqValue struct {
	number     uint64
	data       []byte
	fields     []aqValue
	descriptor uint64
	null       bool
}

func aqDecode(t *testing.T, r *bytes.Reader, depth int) aqValue {
	t.Helper()
	if depth > 16 {
		t.Fatal("AMQP fixture nesting")
	}
	take := func(n int) []byte {
		if n < 0 || n > 65536 || n > r.Len() {
			t.Fatal("AMQP fixture length", n, r.Len())
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			t.Fatal(err)
		}
		return b
	}
	code, err := r.ReadByte()
	if err != nil {
		t.Fatal(err)
	}
	v := aqValue{}
	switch code {
	case 0x40:
		v.null = true
	case 0x41:
		v.number = 1
	case 0x42, 0x43, 0x44, 0x45:
	case 0x50, 0x52, 0x53, 0x56:
		v.number = uint64(take(1)[0])
	case 0x60:
		v.number = uint64(binary.BigEndian.Uint16(take(2)))
	case 0x70:
		v.number = uint64(binary.BigEndian.Uint32(take(4)))
	case 0x80, 0x83:
		v.number = binary.BigEndian.Uint64(take(8))
	case 0x98:
		v.data = take(16)
	case 0xa0, 0xa1, 0xa3:
		v.data = take(int(take(1)[0]))
	case 0xb0, 0xb1, 0xb3:
		v.data = take(int(binary.BigEndian.Uint32(take(4))))
	case 0xc0, 0xc1, 0xd0, 0xd1:
		width := 1
		if code >= 0xd0 {
			width = 4
		}
		size := 0
		if width == 4 {
			size = int(binary.BigEndian.Uint32(take(4)))
		} else {
			size = int(take(1)[0])
		}
		body := bytes.NewReader(take(size))
		var count uint32
		if width == 1 {
			b, e := body.ReadByte()
			if e != nil {
				t.Fatal(e)
			}
			count = uint32(b)
		} else {
			if err := binary.Read(body, binary.BigEndian, &count); err != nil {
				t.Fatal(err)
			}
		}
		if count > 1024 {
			t.Fatal("AMQP fixture count", count)
		}
		for i := uint32(0); i < count; i++ {
			v.fields = append(v.fields, aqDecode(t, body, depth+1))
		}
		if body.Len() != 0 {
			t.Fatal("AMQP fixture list trailing", body.Len())
		}
	case 0xe0:
		v.data = take(int(take(1)[0])) // SASL mechanisms array, no authority derived.
	case 0xf0:
		v.data = take(int(binary.BigEndian.Uint32(take(4))))
	case 0:
		descriptor := aqDecode(t, r, depth+1)
		v = aqDecode(t, r, depth+1)
		v.descriptor = descriptor.number
	default:
		t.Fatalf("AMQP fixture unsupported encoding %x", code)
	}
	return v
}

type aqPerformative struct {
	kind    uint64
	fields  []aqValue
	payload []byte
}

func aqRead(t *testing.T, conn net.Conn) aqPerformative {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for {
		var header [8]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			t.Fatal(err)
		}
		size := binary.BigEndian.Uint32(header[:4])
		if size < 8 || size > 65536 || header[4] != 2 || header[6] != 0 || header[7] != 0 {
			t.Fatal("AMQP fixture frame", header)
		}
		body := make([]byte, size-8)
		if _, err := io.ReadFull(conn, body); err != nil {
			t.Fatal(err)
		}
		if len(body) == 0 {
			continue
		}
		r := bytes.NewReader(body)
		v := aqDecode(t, r, 0)
		payload := make([]byte, r.Len())
		_, _ = io.ReadFull(r, payload)
		return aqPerformative{kind: v.descriptor, fields: v.fields, payload: payload}
	}
}
func aqField(p aqPerformative, i int) aqValue {
	if i < len(p.fields) {
		return p.fields[i]
	}
	return aqValue{null: true}
}
