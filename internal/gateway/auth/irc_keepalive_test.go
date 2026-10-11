//go:build unix

package auth

import (
	"crypto/tls"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

// ircKeepaliveScript behaves like an idle real server: it sends nothing until
// probed, answers keepalive PINGs (unless dead) and only then publishes.
func ircKeepaliveScript(conn *tls.Conn, reader *ircwire.Reader, mode string) error {
	for range 2 {
		c, err := reader.Read()
		if err != nil || c.Name != "PING" || len(c.Params) != 1 || c.Params[0] != "airc-keepalive" {
			return errors.New("missing keepalive PING")
		}
		if mode == "keepalive_dead" {
			return nil
		}
		if _, err = io.WriteString(conn, ":server PONG server :airc-keepalive\r\n"); err != nil {
			return err
		}
	}
	stamp := time.Now().UTC().Add(-time.Second).Format("2006-01-02T15:04:05.000Z")
	_, err := io.WriteString(conn, "@account=alice;msgid=after-idle;time="+stamp+" :alice!u@h PRIVMSG #external :after idle\r\n")
	return err
}

func TestIRCIdleSessionKeepaliveSurvivesQuietLink(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "keepalive")
	server.keepalive, server.keepaliveTimeout = 300*time.Millisecond, 300*time.Millisecond
	session, err := server.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	started := time.Now()
	var event VerifiedIRCEvent
	for event, err = session.ReadEvent(); errors.Is(err, ErrIRCEvent); event, err = session.ReadEvent() {
	}
	if err != nil || !event.Valid() || event.body != "after idle" {
		t.Fatal("idle session lost", err)
	}
	if elapsed := time.Since(started); elapsed < 600*time.Millisecond {
		t.Fatal("event arrived before two quiet keepalive intervals", elapsed)
	}
}

func TestIRCIdleSessionClosesWithoutKeepaliveReply(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "keepalive_dead")
	server.keepalive, server.keepaliveTimeout = 200*time.Millisecond, 300*time.Millisecond
	session, err := server.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	started := time.Now()
	_, err = session.ReadEvent()
	elapsed := time.Since(started)
	if !errors.Is(err, ircwire.ErrIdle) || elapsed < 500*time.Millisecond || elapsed > 2*time.Second {
		t.Fatal("dead link detection", elapsed, err)
	}
	if _, err = session.ReadEvent(); !errors.Is(err, ErrAuthentication) {
		t.Fatal("dead session reused", err)
	}
}
