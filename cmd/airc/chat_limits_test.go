package main

import (
	"context"
	"github.com/Someblueman/airc/pkg/irc"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func adminChat(t *testing.T, address string, r irc.ChatRequest) []irc.ChatEntry {
	t.Helper()
	c, err := irc.Dial(irc.Config{Nick: "admin-test", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.AuthenticateAdmin(strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entries, err := irc.RequestChat(ctx, c, r, nil)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestRoomSlowModeAcrossReconnectsAllowsRetriesAndReactions(t *testing.T) {
	address := chatServer(t, 32)
	adminChat(t, address, irc.ChatRequest{Action: "room", Target: "#room", Seconds: 1, Limit: -1})
	args := []string{"send", "--nick", "bot", "--channel", "room", "--message", "first", "--request-id", "unique"}
	mustCLI(t, address, args...)
	mustCLI(t, address, args...)
	deniedCLI(t, address, "retry after", "send", "--nick", "bot", "--channel", "room", "--message", "second")
	root := posted(t, address, "other", "#room", "question")
	mustCLI(t, address, "react", root.ID, "seen", "--nick", "bot")
	mustCLI(t, address, "check", "--nick", "bot", "--channel", "room")
	// Slow mode is whole seconds, so the interval cannot be shortened; retry the
	// send until it is admitted rather than guessing a sleep. Denied attempts
	// post nothing.
	eventuallyEvery(t, 5*time.Second, 50*time.Millisecond, "slow mode to admit the second message", func() bool {
		_, _, err := cli(t, address, "send", "--nick", "bot", "--channel", "room", "--message", "second")
		return err == nil
	})
	c, err := irc.Dial(irc.Config{Nick: "guest", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := irc.RequestChat(ctx, c, irc.ChatRequest{Action: "room", Target: "#room", Seconds: 0, Limit: -1}, nil); err == nil {
		t.Fatal("guest changed slow mode")
	}
}

func TestRoomQuotaPreservesQuietHistory(t *testing.T) {
	address := chatServer(t, 6)
	adminChat(t, address, irc.ChatRequest{Action: "room", Target: "#noisy", Seconds: -1, Limit: 3})
	quiet := posted(t, address, "quiet", "#quiet", "preserve me")
	for i := 0; i < 12; i++ {
		posted(t, address, "loud", "#noisy", "traffic")
	}
	if out := mustCLI(t, address, "history", "#quiet", "--json"); !strings.Contains(out, quiet.ID) {
		t.Fatal(out)
	}
	if out := mustCLI(t, address, "history", "#noisy", "--json"); strings.Count(out, "traffic") != 3 {
		t.Fatal(out)
	}
}

func TestCredentialsMustBePrivateAndWrongTokensCannotAuthenticate(t *testing.T) {
	address := chatServer(t, 8)
	mustCLI(t, address, "user", "create", "--nick", "fixed")
	path, err := identityPath(options{nick: "fixed", addr: address})
	if err != nil {
		t.Fatal(err)
	}
	value, err := loadIdentity(path, options{nick: "fixed", addr: address})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := irc.Dial(irc.Config{Nick: "fixed", Addr: address, Ephemeral: true, IdentityToken: strings.Repeat("f", 64)}); err == nil {
		c.Close()
		t.Fatal("wrong credential accepted")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadIdentity(path, options{nick: "fixed", addr: address}); err == nil {
		t.Fatal("public credential accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "identity")
	if data, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if err := os.WriteFile(other, data, 0600); err != nil {
		t.Fatal(err)
	}
	if value.Token == "" {
		t.Fatal("empty token")
	}
	mustCLI(t, address, "send", "--identity", other, "--channel", "room", "--message", "from copied identity")
	for _, args := range [][]string{{"history", "#room"}, {"directory"}, {"doctor"}, {"topic", "#room"}} {
		mustCLI(t, address, append(args, "--identity", other)...)
	}
}
