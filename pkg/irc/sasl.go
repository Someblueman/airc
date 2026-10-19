package irc

import (
	"encoding/base64"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/protocol"
)

// saslLogin is the whole SASL PLAIN login, sent before NICK/USER so
// credentials stay out of chat events. aircd reads a connection's lines in
// order, so the client sends it at once rather than waiting for each reply:
// the login then adds no round trip to registration's own, where waiting
// cost three.
func (c *Client) saslLogin() string {
	var lines strings.Builder
	lines.WriteString("CAP REQ :sasl\r\nAUTHENTICATE PLAIN\r\n")
	payload := base64.StdEncoding.EncodeToString([]byte("\x00" + c.currentNick() + "\x00" + c.cfg.IdentityToken))
	for len(payload) >= 400 {
		lines.WriteString("AUTHENTICATE " + payload[:400] + "\r\n")
		payload = payload[400:]
	}
	if payload == "" {
		payload = "+"
	}
	lines.WriteString("AUTHENTICATE " + payload + "\r\nCAP END\r\n")
	return lines.String()
}

// saslReply reads one reply to the login: done once the server confirms it,
// an error once it refuses it, and otherwise neither.
func saslReply(cmd *protocol.Command) (done bool, err error) {
	switch cmd.Name {
	case "CAP":
		if verb, _ := cmd.Param(1); verb == "NAK" {
			return false, errors.New("server does not support SASL login")
		}
	case "AUTHENTICATE":
		if challenge, _ := cmd.Param(0); challenge != "+" {
			return false, errors.New("unexpected SASL challenge")
		}
	case "903":
		return true, nil
	case "904", "905", "906", "907", "464", "421", "451", "ERROR":
		return false, &RejectedError{Code: cmd.Name, Message: "SASL login rejected: " + cmd.Trailing, Throttled: throttled(cmd.Params)}
	}
	return false, nil
}
