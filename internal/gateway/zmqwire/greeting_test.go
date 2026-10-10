package zmqwire

import (
	"bytes"
	"testing"
)

func TestGreeting(t *testing.T) {
	var literal [64]byte
	literal[0], literal[9], literal[10], literal[11] = 255, 127, 3, 1
	copy(literal[12:], "NULL")
	if Greeting() != literal {
		t.Fatal("unexpected greeting")
	}
	for i := 0; i < 64; i++ {
		if err := ReadGreeting(bytes.NewReader(literal[:i])); err == nil {
			t.Fatalf("accepted truncation %d", i)
		}
	}
	for _, version := range [][2]byte{{3, 1}, {3, 255}, {4, 0}} {
		b := literal
		b[10], b[11] = version[0], version[1]
		for i := 1; i < 9; i++ {
			b[i] = byte(i)
		}
		r := bytes.NewReader(append(b[:], 99))
		if err := ReadGreeting(r); err != nil || r.Len() != 1 {
			t.Fatalf("version %v: %v", version, err)
		}
	}
	for _, index := range []int{0, 9, 10, 11, 12, 16, 31, 32, 33, 63} {
		b := literal
		b[index] ^= 1
		if index == 10 {
			b[index] = 2
		}
		if err := ReadGreeting(bytes.NewReader(b[:])); err == nil {
			t.Fatalf("accepted field %d", index)
		}
	}
	if err := ReadGreeting(nil); err == nil {
		t.Fatal("nil reader")
	}
}
