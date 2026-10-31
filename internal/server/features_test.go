package server

import (
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// register005 registers a guest on address and returns the 005 line's tokens.
func register005(t *testing.T, address string) []string {
	t.Helper()
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "NICK probe\r\nUSER u 0 * :u\r\n")
	line := expectLine(t, reader, conn, " 005 ")
	expectLine(t, reader, conn, " 001 ")
	if len(line) > protocol.MaxLineLength/8 {
		t.Errorf("005 is %d bytes, longer than the one line a client expects", len(line))
	}
	command, err := protocol.Parse(strings.TrimRight(line, "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	return command.Params[1:]
}

// Test005AdvertisesTheLevelAndTheTokensWithAValue pins the 005 reply: the
// AIRC level stands for every version-only feature, so only the tokens with
// a value, the standard IRC ones and those that depend on configuration are
// sent, and every feature in protocol.Features up to protocol.Level is had.
func Test005AdvertisesTheLevelAndTheTokensWithAValue(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}, nil)
	got := strings.Join(register005(t, address), " ")
	want := "AIRC=1 MONITOR=128 AWAYLEN=240 PREFIX=(o)@ CHANMODES=,,, HISTORY=8 PASTE_BYTES=65536 IDEMPOTENCY=1 SAFE_RETRY=1"
	if got != want {
		t.Fatalf("005 = %q\n   want %q", got, want)
	}
	features := map[string]string{}
	for _, token := range strings.Fields(got) {
		key, value, _ := strings.Cut(token, "=")
		features[key] = value
	}
	for name := range protocol.Features {
		if !protocol.Supports(features, name) {
			t.Errorf("the server lacks %s", name)
		}
	}
	for _, name := range []string{"ADMIN", "WAKE", "ACCOUNTS", "ACCESS", "TLS", "WEBIRC", "CARRIERS"} {
		if protocol.Supports(features, name) {
			t.Errorf("an unconfigured server advertises %s", name)
		}
	}

	// Without history there is nothing to retry against.
	address, _ = startServer(t, Config{HistoryLimit: 0, PingInterval: time.Hour, ReadTimeout: time.Hour}, nil)
	if got := strings.Join(register005(t, address), " "); got != "AIRC=1 MONITOR=128 AWAYLEN=240 PREFIX=(o)@ CHANMODES=,,, HISTORY=0 PASTE_BYTES=65536" {
		t.Fatalf("005 without history = %q", got)
	}

	// Configuration adds its tokens between the fixed ones and PASTE_BYTES.
	address, _ = adminWireServer(t)
	if got := strings.Join(register005(t, address), " "); got != "AIRC=1 MONITOR=128 AWAYLEN=240 PREFIX=(o)@ CHANMODES=,,, HISTORY=32 ADMIN=1 PASTE_BYTES=65536 IDEMPOTENCY=1 SAFE_RETRY=1" {
		t.Fatalf("005 with an admin credential = %q", got)
	}
}
