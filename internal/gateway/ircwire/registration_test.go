package ircwire

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func regCommand(t *testing.T, line string) protocol.Command {
	t.Helper()
	c, err := Parse([]byte(line + "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func regFixture(t *testing.T) *Registration {
	t.Helper()
	r, err := NewRegistration("bridge", "bridge-account", []byte("password"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	wires, err := r.Start()
	if err != nil || string(bytes.Join(wires, nil)) != "CAP REQ :sasl account-tag message-tags server-time\r\nNICK bridge\r\nUSER bridge 0 * :AIRC bridge\r\n" {
		t.Fatal(wires, err)
	}
	return r
}
func regStep(t *testing.T, r *Registration, line string) [][]byte {
	t.Helper()
	wires, err := r.Step(regCommand(t, line))
	if err != nil {
		t.Fatal(line, err)
	}
	return wires
}
func TestRegistrationRequiredCapabilitiesAndAccount(t *testing.T) {
	r := regFixture(t)
	if r.Ready() {
		t.Fatal("early readiness")
	}
	if wires := regStep(t, r, ":server NOTICE * :Looking up hostname"); len(wires) != 0 {
		t.Fatal(wires)
	}
	if wires := regStep(t, r, "PING :keep alive"); string(bytes.Join(wires, nil)) != "PONG :keep alive\r\n" {
		t.Fatal(wires)
	}
	wires := regStep(t, r, ":server CAP bridge ACK :server-time sasl message-tags account-tag")
	if string(bytes.Join(wires, nil)) != "AUTHENTICATE PLAIN\r\n" {
		t.Fatal(wires)
	}
	wires = regStep(t, r, "AUTHENTICATE +")
	c := regCommand(t, strings.TrimSuffix(string(wires[0]), "\r\n"))
	raw, err := base64.StdEncoding.DecodeString(c.Params[0])
	if err != nil || string(raw) != "bridge-account\x00bridge-account\x00password" || len(r.password) != 0 {
		t.Fatal("SASL data shape", err)
	}
	regStep(t, r, ":server 900 bridge bridge!user@host bridge-account :Logged in")
	if r.Ready() {
		t.Fatal("account numeric granted readiness")
	}
	wires = regStep(t, r, ":server 903 bridge :SASL successful")
	if string(bytes.Join(wires, nil)) != "CAP END\r\n" || r.Ready() {
		t.Fatal(wires)
	}
	regStep(t, r, ":server 001 bridge :Welcome")
	if !r.Ready() {
		t.Fatal("missing readiness")
	}
	if _, err = r.Step(regCommand(t, ":server CAP bridge DEL :account-tag")); err != ErrRegistration || r.Ready() {
		t.Fatal("removed authority capability retained", err)
	}
}

func TestRegistrationOutOfOrderAndRefusals(t *testing.T) {
	for _, line := range []string{":server 001 bridge :Welcome", "AUTHENTICATE +", ":server 903 bridge :Success", ":server CAP * NAK :sasl", ":server CAP * ACK :sasl account-tag message-tags", ":server CAP * ACK :sasl account-tag message-tags server-time extra", ":server CAP * ACK :sasl sasl account-tag server-time", ":user!u@h CAP * ACK :sasl account-tag message-tags server-time", ":server 433 * bridge :Nickname in use", ":server 432 * bridge :Erroneous nickname", ":server 904 * :Failed", ":server 902 * :Nick locked", ":server 904 bridge :Failed", ":server 901 bridge bridge!u@h :Logged out", ":alice!u@h PRIVMSG #room :too early"} {
		t.Run(line, func(t *testing.T) {
			r := regFixture(t)
			if wire, err := r.Step(regCommand(t, line)); err != ErrRegistration || wire != nil || r.Ready() || len(r.password) != 0 {
				t.Fatal("registration bypass", err)
			}
			if _, err := r.Step(regCommand(t, "PING :x")); err != ErrRegistration {
				t.Fatal("failed session reused")
			}
		})
	}
	for _, line := range []string{":server 900 bridge bridge!u@h other-account :Logged in", ":alice!u@h 900 bridge bridge!u@h bridge-account :Logged in", "AUTHENTICATE other"} {
		r := regFixture(t)
		regStep(t, r, ":server CAP * ACK :sasl account-tag message-tags server-time")
		regStep(t, r, "AUTHENTICATE +")
		if _, err := r.Step(regCommand(t, line)); err != ErrRegistration {
			t.Fatal(line, err)
		}
	}
	r := regFixture(t)
	for range 128 {
		regStep(t, r, ":server NOTICE * :noise")
	}
	if _, err := r.Step(regCommand(t, "PING :x")); err != ErrRegistration {
		t.Fatal("unbounded registration noise")
	}
}

func TestRegistrationAcceptsUnregisteredSASLTargetAndInformationalNumerics(t *testing.T) {
	r := regFixture(t)
	regStep(t, r, ":server CAP * ACK :sasl account-tag message-tags server-time")
	regStep(t, r, "AUTHENTICATE +")
	regStep(t, r, ":server 900 * * bridge-account :You are now logged in")
	if wires := regStep(t, r, ":server 903 * :Authentication successful"); string(bytes.Join(wires, nil)) != "CAP END\r\n" {
		t.Fatal(wires)
	}
	regStep(t, r, ":server 001 bridge :Welcome")
	for _, line := range []string{":server 422 bridge :MOTD File is missing", ":server 401 bridge ghost :No such nick", ":server 433 bridge other :Nickname in use", ":server 502 bridge :Cannot change mode for other users"} {
		if wires := regStep(t, r, line); len(wires) != 0 || !r.Ready() {
			t.Fatal("informational numeric ended session", line)
		}
	}
	for _, line := range []string{":server 900 other * bridge-account :Logged in", ":server 903 other :Success"} {
		r := regFixture(t)
		regStep(t, r, ":server CAP * ACK :sasl account-tag message-tags server-time")
		regStep(t, r, "AUTHENTICATE +")
		if _, err := r.Step(regCommand(t, line)); err != ErrRegistration {
			t.Fatal("foreign SASL target", line, err)
		}
	}
	r = regFixture(t)
	if wires := regStep(t, r, ":server 422 * :MOTD File is missing"); len(wires) != 0 {
		t.Fatal(wires)
	}
}

func TestSASLAccountAndSuccessMayArriveInEitherOrder(t *testing.T) {
	r := regFixture(t)
	regStep(t, r, ":server CAP * ACK :sasl account-tag message-tags server-time")
	regStep(t, r, "AUTHENTICATE +")
	if wires := regStep(t, r, ":server 903 bridge :Success"); len(wires) != 0 || r.Ready() {
		t.Fatal("success without account confirmation")
	}
	wires := regStep(t, r, ":server 900 bridge bridge!u@h bridge-account :Logged in")
	if string(bytes.Join(wires, nil)) != "CAP END\r\n" {
		t.Fatal(wires)
	}
	regStep(t, r, ":server 001 bridge :Welcome")
	if !r.Ready() {
		t.Fatal("reordered login failed")
	}
}
