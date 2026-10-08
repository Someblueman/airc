package zmqwire

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"testing"
)

func property(name string, value []byte) []byte {
	b := append([]byte{byte(len(name))}, name...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(value)))
	return append(b, value...)
}
func readyFrame(body []byte) []byte {
	b := binary.BigEndian.AppendUint64([]byte{6}, uint64(len(body)))
	return append(b, body...)
}
func TestReadyLiteral(t *testing.T) {
	literal := []byte{4, 25, 5, 'R', 'E', 'A', 'D', 'Y', 11, 'S', 'o', 'c', 'k', 'e', 't', '-', 'T', 'y', 'p', 'e', 0, 0, 0, 3, 'R', 'E', 'P'}
	if !bytes.Equal(Ready(), literal) {
		t.Fatalf("server ready %x", Ready())
	}
	literal[len(literal)-1] = 'Q'
	for i := 0; i < len(literal); i++ {
		if err := ReadReady(bytes.NewReader(literal[:i])); err == nil {
			t.Fatalf("truncation %d", i)
		}
	}
	r := bytes.NewReader(append(literal, 99))
	if err := ReadReady(r); err != nil || r.Len() != 1 {
		t.Fatalf("client ready %v", err)
	}
	body := append([]byte("\x05READY"), property("socket-type", []byte("REQ"))...)
	body = append(body, property("Identity", []byte{0, 255})...)
	body = append(body, property("Resource", []byte("untrusted"))...)
	if err := ReadReady(bytes.NewReader(readyFrame(body))); err != nil {
		t.Fatal(err)
	}
}
func TestReadyRefusals(t *testing.T) {
	base := append([]byte("\x05READY"), property("Socket-Type", []byte("REQ"))...)
	cases := [][]byte{[]byte("\x05READY"), []byte("\x05ERROR"), append([]byte("\x05READY"), property("Socket-Type", []byte("REP"))...)}
	for _, suffix := range [][]byte{{0}, {1, 'A', 0, 0, 0}, {1, 'A', 255, 255, 255, 255}, property("socket-type", []byte("REQ")), property("Bad Name", nil)} {
		cases = append(cases, append(bytes.Clone(base), suffix...))
	}
	for _, body := range cases {
		if err := ReadReady(bytes.NewReader(readyFrame(body))); err == nil {
			t.Fatalf("accepted %x", body)
		}
	}
	for i := 0; i < 31; i++ {
		base = append(base, property(fmt.Sprintf("X%d", i), nil)...)
	}
	if err := ReadReady(bytes.NewReader(readyFrame(base))); err != nil {
		t.Fatal(err)
	}
	base = append(base, property("extra", nil)...)
	if err := ReadReady(bytes.NewReader(readyFrame(base))); err == nil {
		t.Fatal("property count")
	}
	if err := ReadReady(bytes.NewReader([]byte{0, 6, 5, 'R', 'E', 'A', 'D', 'Y'})); err == nil {
		t.Fatal("data command")
	}
}
