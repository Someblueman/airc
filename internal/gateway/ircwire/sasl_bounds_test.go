package ircwire

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSASLChunkBoundariesAndOwnedCredentials(t *testing.T) {
	for _, n := range []int{295, 296, 297, 1024} {
		password := bytes.Repeat([]byte{'p'}, n)
		r, err := NewRegistration("b", "a", password)
		if err != nil {
			t.Fatal(err)
		}
		clear(password)
		_, _ = r.Start()
		regStep(t, r, ":server CAP * ACK :sasl account-tag message-tags server-time")
		wires := regStep(t, r, "AUTHENTICATE +")
		var data string
		for i, wire := range wires {
			c := regCommand(t, strings.TrimSuffix(string(wire), "\r\n"))
			if len(c.Params) != 1 || len(c.Params[0]) > 400 {
				t.Fatal("chunk bound")
			}
			if c.Params[0] == "+" {
				if i != len(wires)-1 {
					t.Fatal("misplaced terminator")
				}
				continue
			}
			data += c.Params[0]
		}
		raw, err := base64.StdEncoding.DecodeString(data)
		if err != nil || string(raw) != "a\x00a\x00"+strings.Repeat("p", n) {
			t.Fatal("whole credentials", n, err)
		}
		last := regCommand(t, strings.TrimSuffix(string(wires[len(wires)-1]), "\r\n")).Params[0]
		if len(data)%400 == 0 && last != "+" || len(data)%400 != 0 && last == "+" {
			t.Fatal("SASL terminal chunk", n)
		}
		r.Close()
		if r.Ready() || len(r.password) != 0 {
			t.Fatal("closed credentials retained")
		}
	}
}

func TestRegistrationConstructorAndLifecycle(t *testing.T) {
	for _, p := range [][]byte{nil, []byte("bad\x00"), []byte("bad\r"), []byte("\xff"), bytes.Repeat([]byte{'x'}, 1025)} {
		if r, err := NewRegistration("bridge", "account", p); err != ErrRegistration || r != nil {
			t.Fatal("invalid credentials")
		}
	}
	for _, n := range []string{"", strings.Repeat("x", 31), "bad name", "bad\r"} {
		if r, err := NewRegistration(n, "account", []byte("p")); err != ErrRegistration || r != nil {
			t.Fatal("invalid nickname")
		}
	}
	var r *Registration
	if r.Ready() {
		t.Fatal("nil ready")
	}
	r.Close()
	if _, err := r.Start(); err != ErrRegistration {
		t.Fatal(err)
	}
	r = regFixture(t)
	if _, err := r.Start(); err != ErrRegistration {
		t.Fatal("duplicate start")
	}
	r.Close()
	if _, err := r.Start(); err != ErrRegistration {
		t.Fatal("closed restart")
	}
}
