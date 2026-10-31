package irc_test

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"fmt"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// loginServer answers nothing until the client has sent USER, then replies
// with replies, and returns the lines it read before replying. A client that
// waited for a reply mid-login would never send USER.
func loginServer(t *testing.T, replies ...string) (string, <-chan []string) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	read := make(chan []string, 1)
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var lines []string
		for r := bufio.NewScanner(conn); r.Scan(); {
			lines = append(lines, r.Text())
			if strings.HasPrefix(r.Text(), "USER ") {
				break
			}
		}
		read <- lines
		for _, reply := range replies {
			if _, err := conn.Write([]byte(reply + "\r\n")); err != nil {
				return
			}
		}
		_, _ = conn.Read(make([]byte, 1)) // until the client closes
	}()
	return l.Addr().String(), read
}

func TestAccountLoginIsSentWithRegistration(t *testing.T) {
	token := strings.Repeat("c", 64)
	for _, tc := range []struct {
		name    string
		replies []string
		refused string
	}{
		{"confirmed", []string{":server CAP * ACK :sasl", "AUTHENTICATE +", ":server 903 agent :SASL authentication successful", fmt.Sprintf(":server 005 agent AIRC=%d :features", protocol.Level), ":server 001 agent :Welcome"}, ""},
		{"refused", []string{":server CAP * ACK :sasl", "AUTHENTICATE +", ":server 904 agent :Invalid SASL credentials", ":server 001 agent :Welcome"}, "Invalid SASL credentials"},
		{"unsupported", []string{":server CAP * NAK :sasl", ":server 001 agent :Welcome"}, "not support SASL"},
		{"unconfirmed", []string{":server CAP * ACK :sasl", "AUTHENTICATE +", ":server 001 agent :Welcome"}, "without confirming its login"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			address, read := loginServer(t, tc.replies...)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client, err := irc.DialContext(ctx, irc.Config{Nick: "agent", Addr: address, IdentityToken: token, Ephemeral: true})
			lines := <-read
			want := []string{"CAP REQ :sasl", "AUTHENTICATE PLAIN", "AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte("\x00agent\x00"+token)), "CAP END", "EPHEMERAL", "NICK agent"}
			if len(lines) != len(want)+1 || !slices.Equal(lines[:len(want)], want) {
				t.Fatalf("the client sent %q before any reply", lines)
			}
			if tc.refused == "" {
				if err != nil {
					t.Fatal(err)
				}
				client.Close()
				return
			}
			if err == nil {
				client.Close()
				t.Fatal("registered without a confirmed login")
			}
			var rejected *irc.RejectedError
			if !strings.Contains(err.Error(), tc.refused) || tc.name == "refused" && (!errors.As(err, &rejected) || rejected.Code != "904") || strings.Contains(err.Error(), token) {
				t.Fatalf("login error %v", err)
			}
		})
	}
}

// A REGISTER refused at a limit fails the dial, whether the limit passes
// (437, a creation window) or not (405, the account limit); neither falls
// through to a guest session under the name the client meant to create.
func TestRegistrationAbortsOnLimits(t *testing.T) {
	token := strings.Repeat("c", 64)
	for _, numeric := range []string{"405", "437"} {
		t.Run(numeric, func(t *testing.T) {
			address, read := loginServer(t, ":server "+numeric+" agent :Account limit reached", ":server 001 agent :Welcome")
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			client, err := irc.DialContext(ctx, irc.Config{Nick: "agent", Addr: address, IdentityToken: token, CreateAccount: true, Ephemeral: true})
			if lines := <-read; len(lines) == 0 || lines[0] != "REGISTER agent :"+token {
				t.Fatalf("the client sent %q", lines)
			}
			if err == nil {
				client.Close()
				t.Fatal("a refused registration became a guest session")
			}
			var rejected *irc.RejectedError
			if !errors.As(err, &rejected) || rejected.Code != numeric || !strings.Contains(err.Error(), "Account limit reached") || strings.Contains(err.Error(), token) {
				t.Fatalf("registration error %v", err)
			}
		})
	}
}
