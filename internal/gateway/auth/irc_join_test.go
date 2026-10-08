//go:build unix

package auth

import (
	"crypto/tls"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

func ircJoinScript(conn *tls.Conn, reader *ircwire.Reader, mode string) error {
	c, err := reader.Read()
	if err != nil || c.Name != "JOIN" || len(c.Params) != 1 || c.Params[0] != "#external" {
		return errors.New("room join mismatch")
	}
	line := ":bridge!u@h JOIN :#EXTERNAL\r\n"
	switch mode {
	case "join_refused":
		line = ":server 473 bridge #external :Invite only\r\n"
	case "join_foreign":
		line = ":alice!u@h JOIN :#external\r\n"
	case "join_event":
		line = ":alice!u@h PRIVMSG #external :premature\r\n"
	case "join_controls":
		line = strings.Repeat("PING :noise\r\n", 513)
	}
	_, err = io.WriteString(conn, line)
	if err == nil && mode == "join_multiple" {
		c, err = reader.Read()
		if err != nil || c.Name != "JOIN" || len(c.Params) != 1 || c.Params[0] != "#other" {
			return errors.New("second room mismatch")
		}
		_, err = io.WriteString(conn, ":bridge!u@h JOIN #other\r\n")
	}
	return err
}

func TestIRCJoinRequiresOwnBoundedConfirmation(t *testing.T) {
	for _, mode := range []string{"join_success", "join_duplicate", "join_multiple", "join_refused", "join_foreign", "join_event", "join_controls"} {
		t.Run(mode, func(t *testing.T) {
			server, _, _ := ircSessionFixture(t, mode)
			session, err := server.Connect(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			err = session.JoinRooms()
			if mode == "join_success" || mode == "join_duplicate" || mode == "join_multiple" {
				if err != nil {
					t.Fatal("own confirmation refused", err)
				}
			} else if !errors.Is(err, ErrAuthentication) {
				t.Fatal("incomplete membership accepted", err)
			}
		})
	}
	if err := (*IRCSession)(nil).JoinRooms(); !errors.Is(err, ErrAuthentication) {
		t.Fatal("nil session", err)
	}
}
