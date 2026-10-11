package ircwire

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

var ErrRegistration = errors.New("IRC bridge registration refused")

const requiredCapabilities = "sasl account-tag message-tags server-time"

// Registration describes protocol state only. Readiness grants no authority:
// the connection owner must supply messages read from its verified TLS server.
// It is serial, bounded to 128 server messages and does not own I/O or deadlines.
type Registration struct {
	nickname, account string
	password          []byte
	phase             uint8
	received          int
	loggedIn          bool
	saslSuccess       bool
}

func NewRegistration(nickname, account string, password []byte) (*Registration, error) {
	if len(nickname) < 1 || len(nickname) > 30 || len(account) < 1 || len(account) > 64 || len(password) < 1 || len(password) > 1024 || !utf8.Valid(password) || !utf8.ValidString(account) || strings.ContainsAny(account, "\x00\r\n ") || strings.ContainsAny(string(password), "\x00\r\n") {
		return nil, ErrRegistration
	}
	if _, err := Encode("NICK", []string{nickname}, nil); err != nil {
		return nil, ErrRegistration
	}
	return &Registration{nickname: nickname, account: account, password: append([]byte(nil), password...)}, nil
}

func (r *Registration) Close() {
	if r != nil {
		clear(r.password)
		r.password = nil
		r.phase = 6
	}
}
func (r *Registration) Ready() bool { return r != nil && r.phase == 5 }

func (r *Registration) Start() ([][]byte, error) {
	if r == nil || r.phase != 0 {
		return nil, ErrRegistration
	}
	r.phase = 1
	realName := "AIRC bridge"
	caps := requiredCapabilities
	return [][]byte{mustCommand("CAP", []string{"REQ"}, &caps), mustCommand("NICK", []string{r.nickname}, nil), mustCommand("USER", []string{r.nickname, "0", "*"}, &realName)}, nil
}

func mustCommand(name string, params []string, body *string) []byte {
	// All callers use bounded constants or constructor-validated names/data.
	wire, _ := Encode(name, params, body)
	return wire
}

func (r *Registration) saslClient(target string) bool {
	return target == "*" || target == r.nickname
}

// registrationFailure lists the numerics that refuse a step this client sent
// before welcome. Other numerics, such as 422 (no MOTD) or replies to commands
// never sent, are informational. After welcome only ERROR, SASL failure or
// logout and removed capabilities end the session; other 4xx/5xx replies do not.
func registrationFailure(name string) bool {
	switch name {
	case "410", "421", // invalid CAP subcommand; CAP or AUTHENTICATE unsupported
		"431", "432", "433", "436", "437", // nickname missing, invalid, in use or unavailable
		"451", "461", "462", "464", "465": // registration, password or ban refusal
		return true
	}
	return false
}

func (r *Registration) refuse() ([][]byte, error) { r.Close(); return nil, ErrRegistration }

// Step accepts only the selected PLAIN exchange; failed or out-of-order login
// cannot fall back to an unauthenticated registration. Returned wires are owned.
func (r *Registration) Step(c protocol.Command) ([][]byte, error) {
	if r == nil || r.phase == 0 || r.phase == 6 {
		return nil, ErrRegistration
	}
	if r.phase != 5 {
		r.received++
		if r.received > 128 {
			return r.refuse()
		}
	}
	if c.Name == "PING" {
		if len(c.Params) == 1 && !c.HasTrailing {
			wire, err := Encode("PONG", c.Params, nil)
			if err != nil {
				return r.refuse()
			}
			return [][]byte{wire}, nil
		}
		if len(c.Params) == 0 && c.HasTrailing {
			body := c.Trailing
			wire, err := Encode("PONG", nil, &body)
			if err != nil {
				return r.refuse()
			}
			return [][]byte{wire}, nil
		}
		return r.refuse()
	}
	if (c.Name == "CAP" || c.Name == "900" || c.Name == "903" || c.Name == "001") && strings.ContainsAny(c.Prefix, "!@") {
		return r.refuse()
	}
	switch c.Name {
	case "CAP":
		if len(c.Params) < 2 || c.Params[0] != "*" && c.Params[0] != r.nickname {
			return r.refuse()
		}
		sub := c.Params[1]
		if sub == "DEL" {
			for cap := range strings.FieldsSeq(c.Text()) {
				if strings.Contains(" "+requiredCapabilities+" ", " "+cap+" ") {
					return r.refuse()
				}
			}
			return nil, nil
		}
		if sub != "ACK" || r.phase != 1 || c.HasTrailing && len(c.Params) != 2 || !c.HasTrailing && len(c.Params) != 3 {
			return r.refuse()
		}
		seen := make(map[string]bool, 4)
		for cap := range strings.FieldsSeq(c.Text()) {
			if seen[cap] || !strings.Contains(" "+requiredCapabilities+" ", " "+cap+" ") {
				return r.refuse()
			}
			seen[cap] = true
		}
		if len(seen) != 4 {
			return r.refuse()
		}
		r.phase = 2
		return [][]byte{mustCommand("AUTHENTICATE", []string{"PLAIN"}, nil)}, nil
	case "AUTHENTICATE":
		if r.phase != 2 || c.HasTrailing || len(c.Params) != 1 || c.Params[0] != "+" {
			return r.refuse()
		}
		raw := append([]byte(r.account), 0)
		raw = append(raw, r.account...)
		raw = append(raw, 0)
		raw = append(raw, r.password...)
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
		base64.StdEncoding.Encode(encoded, raw)
		clear(raw)
		clear(r.password)
		r.password = nil
		defer clear(encoded)
		var out [][]byte
		for len(encoded) > 0 {
			n := min(400, len(encoded))
			out = append(out, mustCommand("AUTHENTICATE", []string{string(encoded[:n])}, nil))
			encoded = encoded[n:]
			if n == 400 && len(encoded) == 0 {
				out = append(out, mustCommand("AUTHENTICATE", []string{"+"}, nil))
			}
		}
		r.phase = 3
		return out, nil
	case "900":
		// Before welcome the IRCv3 SASL client parameter may be "*".
		if r.phase != 3 || len(c.Params) != 3 || !r.saslClient(c.Params[0]) || c.Params[2] != r.account || r.loggedIn {
			return r.refuse()
		}
		r.loggedIn = true
		if r.saslSuccess {
			r.phase = 4
			return [][]byte{mustCommand("CAP", []string{"END"}, nil)}, nil
		}
		return nil, nil
	case "903":
		if r.phase != 3 || r.saslSuccess || len(c.Params) != 1 || !r.saslClient(c.Params[0]) {
			return r.refuse()
		}
		r.saslSuccess = true
		if !r.loggedIn {
			return nil, nil
		}
		r.phase = 4
		return [][]byte{mustCommand("CAP", []string{"END"}, nil)}, nil
	case "001":
		if r.phase != 4 || len(c.Params) != 1 || c.Params[0] != r.nickname {
			return r.refuse()
		}
		r.phase = 5
		return nil, nil
	case "901", "902", "904", "905", "906", "907", "ERROR":
		return r.refuse()
	}
	if r.phase != 5 && registrationFailure(c.Name) {
		return r.refuse()
	}
	if r.phase != 5 && (c.Name == "PRIVMSG" || c.Name == "NOTICE" && c.Tags["account"] != "") {
		return r.refuse()
	}
	return nil, nil
}
