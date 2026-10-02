package irc

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
)

// Login is negotiated before NICK/USER, keeping credentials out of chat events.
func (c *Client) loginSASL(ctx context.Context, scanner *bufio.Scanner) error {
	if err := c.writeLine("CAP REQ :sasl\r\n"); err != nil {
		return err
	}
	authenticated := false
	for scanner.Scan() {
		cmd, err := c.dispatch(ctx, scanner.Text())
		if err != nil {
			return err
		}
		switch cmd.Name {
		case "CAP":
			verb, _ := cmd.Param(1)
			if verb == "NAK" {
				return fmt.Errorf("server does not support SASL login")
			}
			if verb == "ACK" {
				if err := c.writeLine("AUTHENTICATE PLAIN\r\n"); err != nil {
					return err
				}
			}
		case "AUTHENTICATE":
			challenge, _ := cmd.Param(0)
			if challenge != "+" {
				return fmt.Errorf("unexpected SASL challenge")
			}
			payload := base64.StdEncoding.EncodeToString([]byte("\x00" + c.currentNick() + "\x00" + c.cfg.IdentityToken))
			for len(payload) >= 400 {
				if err := c.writeLine("AUTHENTICATE " + payload[:400] + "\r\n"); err != nil {
					return err
				}
				payload = payload[400:]
			}
			if payload == "" {
				payload = "+"
			}
			if err := c.writeLine("AUTHENTICATE " + payload + "\r\n"); err != nil {
				return err
			}
		case "903":
			authenticated = true
		case "904", "905", "906", "907", "464", "421", "451", "ERROR":
			return fmt.Errorf("SASL login rejected: %s", cmd.Trailing)
		}
		if authenticated {
			return c.writeLine("CAP END\r\n")
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("server closed connection during SASL login")
}
