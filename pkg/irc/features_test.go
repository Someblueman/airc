package irc_test

import (
	"bufio"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// tokenServer is a fake daemon that answers registration with the given
// 005 tokens and nothing else.
func tokenServer(t *testing.T, tokens string) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.HasPrefix(line, "USER") {
				io.WriteString(conn, ":server 005 c "+tokens+" :are supported by this server\r\n:server 001 c :Welcome\r\n")
			}
		}
	}()
	return listener.Addr().String()
}

func TestSupportsAnswersFromTheLevelOrTheTokens(t *testing.T) {
	// A current daemon: one level token, value tokens and configuration-dependent ones.
	current, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, "AIRC=1 MONITOR=128 HISTORY=1000 PASTE_BYTES=65536 IDEMPOTENCY=1 SAFE_RETRY=1")})
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	for _, feature := range []string{"CHAT", "STATUS_POSTS", "RESOLVE", "PINS", "HISTORY_START", "multiline", "IDEMPOTENCY", "MONITOR"} {
		if !current.Supports(feature) {
			t.Errorf("a level-1 daemon lacks %s", feature)
		}
	}
	for _, feature := range []string{"ADMIN", "WAKE", "ACCOUNTS", "AIRC2"} {
		if current.Supports(feature) {
			t.Errorf("a level-1 daemon without the token has %s", feature)
		}
	}
	if current.Level() != 1 || current.Features()["PASTE_BYTES"] != "65536" || current.Features()["AIRC"] != "1" {
		t.Errorf("level %d, features %v", current.Level(), current.Features())
	}

	// A daemon from before levels: one token per feature, and CHAT=1 implied
	// pins, polls and room settings but not the later status posts.
	older, err := irc.Dial(irc.Config{Nick: "c", Addr: tokenServer(t, "CHAT=1 CONTEXT=1 MULTILINE=1 HISTORY=0 SEARCH=1")})
	if err != nil {
		t.Fatal(err)
	}
	defer older.Close()
	for _, feature := range []string{"CHAT", "CONTEXT", "MULTILINE", "SEARCH", "PINS", "POLLS", "ROOM"} {
		if !older.Supports(feature) {
			t.Errorf("an older CHAT=1 daemon lacks %s", feature)
		}
	}
	for _, feature := range []string{"STATUS_POSTS", "RESOLVE", "CHARTER", "CONTEXT_BYTES", "HANDOFF", "HISTORY", "AIRC"} {
		if older.Supports(feature) {
			t.Errorf("an older CHAT=1 daemon has %s", feature)
		}
	}
	if older.Level() != 0 || older.Features()["HISTORY"] != "0" {
		t.Errorf("level %d, features %v", older.Level(), older.Features())
	}
}

func TestServerAdvertisesItsLevel(t *testing.T) {
	address := startServer(t, server.Config{HistoryLimit: 16}, nil)
	client := newClient(t, "alice", address)
	if client.Level() != protocol.Level {
		t.Fatalf("level %d, want %d: %v", client.Level(), protocol.Level, client.Features())
	}
	features := client.Features()
	for _, name := range []string{"CHAT", "CHARTER", "STATUS_POSTS", "HISTORY_START", "DM_AUDIT", "IDEMPOTENCY", "SAFE_RETRY"} {
		if !client.Supports(name) {
			t.Errorf("the server lacks %s: %v", name, features)
		}
		if _, literal := features[name]; literal && name != "IDEMPOTENCY" && name != "SAFE_RETRY" {
			t.Errorf("%s is still a token of its own: %v", name, features)
		}
	}
	if features["HISTORY"] != "16" || features["AIRC"] != "1" || client.Supports("ADMIN") || client.Supports("WAKE") {
		t.Errorf("features = %v", features)
	}
	if missing := protocol.Missing(features, protocol.Level); len(missing) != 0 {
		t.Errorf("the server lacks %v", missing)
	}
}
