//go:build unix

package auth

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func runIRCScript(raw net.Conn, security *tls.Config, mode string, entered chan struct{}, done chan error) {
	var err error
	nickname := "bridge"
	if mode == "join_unicode_nick" {
		nickname = "keeper"
	}
	defer raw.Close()
	conn := tls.Server(raw, security)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if mode == "blocked_tls" {
		close(entered)
		var b [1]byte
		_, err = raw.Read(b[:])
		if err == nil {
			_, err = io.Copy(io.Discard, raw)
		}
		done <- err
		return
	}
	if err = conn.Handshake(); err != nil {
		done <- err
		return
	}
	r := ircwire.NewReader(conn)
	for _, name := range []string{"CAP", "NICK", "USER"} {
		c, e := r.Read()
		if e != nil || c.Name != name {
			done <- errors.New("registration command mismatch")
			return
		}
	}
	close(entered)
	if mode == "trickle_registration" {
		for {
			time.Sleep(100 * time.Millisecond)
			if _, err = io.WriteString(conn, "PING :noise\r\n"); err != nil {
				done <- err
				return
			}
		}
	}
	if mode == "blocked_registration" {
		_, err = io.Copy(io.Discard, conn)
		done <- err
		return
	}
	_, err = io.WriteString(conn, ":server CAP "+nickname+" ACK :sasl account-tag message-tags server-time\r\n")
	if err != nil {
		done <- err
		return
	}
	c, err := r.Read()
	if err != nil || c.Name != "AUTHENTICATE" || len(c.Params) != 1 || c.Params[0] != "PLAIN" {
		done <- errors.New("mechanism mismatch")
		return
	}
	_, err = io.WriteString(conn, "AUTHENTICATE +\r\n")
	if err != nil {
		done <- err
		return
	}
	c, err = r.Read()
	if err != nil || len(c.Params) != 1 {
		done <- errors.New("missing credentials")
		return
	}
	data, err := base64.StdEncoding.DecodeString(c.Params[0])
	if err != nil || string(data) != "bridge-account\x00bridge-account\x00exact-password" {
		clear(data)
		done <- errors.New("credential snapshot mismatch")
		return
	}
	clear(data)
	account := "bridge-account"
	if mode == "wrong_account" {
		account = "other"
	}
	_, err = io.WriteString(conn, ":server 900 "+nickname+" "+nickname+"!user@host "+account+" :Logged in\r\n:server 903 "+nickname+" :Success\r\n")
	if err != nil {
		done <- err
		return
	}
	c, err = r.Read()
	if err != nil || c.Name != "CAP" || len(c.Params) != 1 || c.Params[0] != "END" {
		done <- errors.New("missing CAP END")
		return
	}
	_, err = io.WriteString(conn, ":server 001 "+nickname+" :Welcome\r\n")
	if err == nil && strings.HasPrefix(mode, "join_") {
		err = ircJoinScript(conn, r, mode)
	}
	if err == nil && mode == "events" {
		stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
		for _, line := range []string{
			"@account=bridge-account;msgid=loop;time=" + stamp + " :bridge!u@h PRIVMSG #external :loop",
			"@+account=alice;msgid=forged;time=" + stamp + " :alice!u@h PRIVMSG #external :forged",
			"@account=alice;msgid=stable;time=" + stamp + " :alice!u@h PRIVMSG #EXTERNAL : exact 世界  ",
			"@account=alice;msgid=stable;time=" + stamp + " :changed-nick!u@h PRIVMSG #external : exact 世界  ",
			":server CAP bridge DEL :account-tag",
		} {
			if _, err = io.WriteString(conn, line+"\r\n"); err != nil {
				break
			}
		}
	}
	if err == nil {
		_, err = io.Copy(io.Discard, conn)
	}
	done <- err
}
