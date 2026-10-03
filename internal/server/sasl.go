package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// CAP holds registration until END. SASL uses the existing account credential;
// it never grants the separate connection-access or administrator privileges.
func (s *Server) capLocked(c *session, cmd protocol.Command) {
	sub, _ := cmd.Param(0)
	nick := c.client.Nick
	if nick == "" {
		nick = "*"
	}
	reply := func(verb, value string) { c.enqueue(":server CAP " + nick + " " + verb + " :" + value + "\r\n") }
	switch strings.ToUpper(sub) {
	case "LS":
		if !c.registered {
			c.capNegotiating = true
		}
		caps := ""
		if s.accountsAt != "" {
			caps = "sasl=PLAIN"
		}
		reply("LS", caps)
	case "LIST":
		caps := ""
		if c.saslEnabled {
			caps = "sasl"
		}
		reply("LIST", caps)
	case "REQ":
		if !c.registered {
			c.capNegotiating = true
		}
		caps := strings.Fields(cmd.Trailing)
		for _, cap := range caps {
			if (cap != "sasl" && cap != "-sasl") || s.accountsAt == "" || c.registered {
				reply("NAK", cmd.Trailing)
				return
			}
		}
		for _, cap := range caps {
			c.saslEnabled = cap == "sasl"
		}
		if !c.saslEnabled {
			c.saslBuffer, c.saslStarted = "", false
		}
		reply("ACK", cmd.Trailing)
	case "END":
		if c.saslStarted {
			s.numericLocked(c, "906", nil, "SASL authentication aborted")
		}
		c.saslBuffer, c.saslStarted, c.capNegotiating = "", false, false
		s.tryRegisterLocked(c)
	default:
		s.numericLocked(c, "410", []string{sub}, "Invalid CAP subcommand")
	}
}

func (s *Server) authenticateLocked(c *session, cmd protocol.Command) {
	value, _ := cmd.Param(0)
	fail := func(code, text string) {
		c.saslBuffer, c.saslStarted = "", false
		s.numericLocked(c, code, nil, text)
	}
	rejected := func() {
		c.saslBuffer, c.saslStarted = "", false
		s.credentialFailedLocked(c, "904", nil, "Invalid SASL credentials")
	}
	if c.accountID != "" {
		fail("907", "Already authenticated")
		return
	}
	if c.registered || !c.saslEnabled {
		fail("904", "Enable SASL before registration")
		return
	}
	if value == "*" {
		fail("906", "SASL authentication aborted")
		return
	}
	if !c.saslStarted {
		if value != "PLAIN" {
			fail("904", "Unsupported SASL mechanism")
			return
		}
		c.saslStarted = true
		c.enqueue("AUTHENTICATE +\r\n")
		return
	}
	if len(value) > 400 || len(c.saslBuffer)+len(value) > 2048 {
		fail("905", "SASL response too long")
		return
	}
	if value != "+" {
		c.saslBuffer += value
	}
	if len(value) == 400 {
		return
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(c.saslBuffer)
	fields := strings.Split(string(decoded), "\x00")
	if err != nil || len(fields) != 3 || (fields[0] != "" && !strings.EqualFold(fields[0], fields[1])) {
		rejected()
		return
	}
	a, exists := s.accounts[nickKey(fields[1])]
	hash := sha256.Sum256([]byte(fields[2]))
	expected, _ := hex.DecodeString(a.Hash)
	if !exists || subtle.ConstantTimeCompare(hash[:], expected) != 1 {
		rejected()
		return
	}
	c.accountID, c.authNick = a.ID, nickKey(a.Nick)
	c.saslBuffer, c.saslStarted = "", false
	s.numericLocked(c, "900", []string{a.Nick + "!" + c.client.Username + "@localhost", a.Nick}, "Logged in as "+a.Nick)
	s.numericLocked(c, "903", nil, "SASL authentication successful")
}
