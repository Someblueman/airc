package nngwire

import (
	"bytes"
	"testing"
)

func TestGreetingExactPeerAndTruncation(t *testing.T) {
	server := [8]byte{0, 0x53, 0x50, 0, 0, 0x31, 0, 0}
	if Greeting() != server {
		t.Fatal("wrong REP0 header")
	}
	peer := []byte{0, 0x53, 0x50, 0, 0, 0x30, 0, 0}
	for i := 0; i < len(peer); i++ {
		if err := ReadGreeting(bytes.NewReader(peer[:i])); err == nil {
			t.Fatalf("accepted truncation %d", i)
		}
	}
	for i := range peer {
		wrong := bytes.Clone(peer)
		wrong[i] ^= 1
		if err := ReadGreeting(bytes.NewReader(wrong)); err == nil {
			t.Fatalf("accepted field %d", i)
		}
	}
	if err := ReadGreeting(bytes.NewReader(server[:])); err == nil {
		t.Fatal("accepted REP peer")
	}
	if err := ReadGreeting(nil); err == nil {
		t.Fatal("nil reader")
	}
	r := bytes.NewReader(append(peer, 99))
	if err := ReadGreeting(r); err != nil || r.Len() != 1 {
		t.Fatalf("header boundary %v remaining %d", err, r.Len())
	}
}
