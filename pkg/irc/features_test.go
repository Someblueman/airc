package irc_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// tokenServer is a fake daemon that registers each connection with the next
// of tokens as its 005 line, the last one again once they run out, and does
// nothing else. With dropFirst it closes the first connection as soon as it
// has registered it, so a Reconnect client dials again.
func tokenServer(t *testing.T, dropFirst bool, tokens ...string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for i := 0; ; i++ {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn, set string, drop bool) {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					if strings.HasPrefix(line, "USER") {
						io.WriteString(conn, ":server 005 c "+set+" :are supported by this server\r\n:server 001 c :Welcome\r\n")
						if drop {
							return
						}
					}
				}
			}(conn, tokens[min(i, len(tokens)-1)], dropFirst && i == 0)
		}
	}()
	return listener.Addr().String()
}

// currentTokens is the 005 of a daemon at this client's level, with the tokens
// that carry a value and those history turns on.
var currentTokens = fmt.Sprintf("AIRC=%d MONITOR=128 HISTORY=1000 PASTE_BYTES=65536 IDEMPOTENCY=1 SAFE_RETRY=1", protocol.Level)

func TestSupportsAnswersFromTheLevelOrTheTokens(t *testing.T) {
	client, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, false, currentTokens)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, feature := range []string{"CHAT", "STATUS_POSTS", "RESOLVE", "PINS", "HISTORY_START", "multiline", "IDEMPOTENCY", "MONITOR"} {
		if !client.Supports(feature) {
			t.Errorf("a daemon at level %d lacks %s", protocol.Level, feature)
		}
	}
	for _, feature := range []string{"ADMIN", "WAKE", "ACCOUNTS", "AIRC2"} {
		if client.Supports(feature) {
			t.Errorf("a daemon without the token has %s", feature)
		}
	}
	if client.Level() != protocol.Level || client.Features()["PASTE_BYTES"] != "65536" || client.Features()["AIRC"] != fmt.Sprint(protocol.Level) {
		t.Errorf("level %d, features %v", client.Level(), client.Features())
	}

	// A newer daemon is used: this client takes nothing it does not know.
	newer, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, false, fmt.Sprintf("AIRC=%d HISTORY=0", protocol.Level+6))})
	if err != nil {
		t.Fatal(err)
	}
	defer newer.Close()
	if newer.Level() != protocol.Level+6 || !newer.Supports("RESOLVE") || newer.Supports("FUTURE") || newer.Supports("HISTORY") {
		t.Errorf("level %d, features %v", newer.Level(), newer.Features())
	}
}

// A daemon that advertises no AIRC level, or one below this client's, is
// refused at the dial: the client needs every feature of its own level.
func TestDialRefusesADaemonBelowTheClientsLevel(t *testing.T) {
	for _, tokens := range []string{"CHAT=1 CONTEXT=1 MULTILINE=1 HISTORY=0", "AIRC=0 HISTORY=8", "AIRC=x"} {
		client, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, false, tokens)})
		if err == nil {
			client.Close()
			t.Fatalf("%q: an older daemon was used", tokens)
		}
		var tooOld *irc.DaemonTooOldError
		if !errors.Is(err, irc.ErrDaemonTooOld) || !errors.As(err, &tooOld) || tooOld.Level != 0 || tooOld.Need != protocol.Level {
			t.Fatalf("%q: %v (%T)", tokens, err, err)
		}
		if err.Error() != "aircd advertises no AIRC level: upgrade aircd and restart it" {
			t.Fatalf("%q: %q", tokens, err)
		}
	}
	if got := (&irc.DaemonTooOldError{Level: 1, Need: 2}).Error(); got != "aircd advertises AIRC level 1; this client needs 2: upgrade aircd and restart it" {
		t.Fatalf("message %q", got)
	}

	// A Reconnect client that finds an older daemon when it dials again
	// says so once and ends, rather than retrying forever.
	client, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, true, currentTokens, "CHAT=1"), Reconnect: true, MinBackoff: 10 * time.Millisecond, MaxBackoff: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	deadline := time.After(5 * time.Second)
	refused := false
	for !refused {
		select {
		case event, ok := <-client.Events():
			if !ok {
				t.Fatal("the connection ended without saying why")
			}
			if c, ok := event.(*irc.ConnectionEvent); ok && !c.Connected && strings.Contains(c.Error, "no AIRC level") {
				refused = true
			}
		case <-deadline:
			t.Fatal("no refusal was reported")
		}
	}
	select {
	case <-client.Done():
	case <-deadline:
		t.Fatal("the client kept reconnecting to an older daemon")
	}
	if !errors.Is(client.Err(), irc.ErrDaemonTooOld) {
		t.Fatalf("ended with %v", client.Err())
	}
}

func TestServerAdvertisesItsLevel(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 16}, nil)
	client := newClient(t, "alice", address)
	if client.Level() != protocol.Level {
		t.Fatalf("level %d, want %d: %v", client.Level(), protocol.Level, client.Features())
	}
	features := client.Features()
	for name := range protocol.Features {
		if !client.Supports(name) {
			t.Errorf("the server lacks %s: %v", name, features)
		}
		if _, literal := features[name]; literal {
			t.Errorf("%s is still a token of its own: %v", name, features)
		}
	}
	if features["HISTORY"] != "16" || features["AIRC"] != fmt.Sprint(protocol.Level) || !client.Supports("IDEMPOTENCY") || !client.Supports("SAFE_RETRY") || client.Supports("ADMIN") || client.Supports("WAKE") {
		t.Errorf("features = %v", features)
	}
}
