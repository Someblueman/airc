package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestUIChannelKickAndOperatorGrants(t *testing.T) {
	address := chatServer(t, 64)
	mustCLI(t, address, "user", "create", "--nick", "me")
	mustCLI(t, address, "user", "create", "--nick", "helper")
	path := filepath.Join(t.TempDir(), "admin")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, address, "op", "--channel", "#room", "--who", "me", "--token-file", path)
	h := startUIBackend(t, address, "#room")
	h.until("startup", func() bool { return h.model.connected })
	h.command("/op helper")
	h.until("grant", func() bool { s, _ := h.model.activeStatus(); return strings.Contains(s, "+o helper") })
	c, err := irc.Dial(irc.Config{Addr: address, Nick: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Join("#room")
	h.until("join", func() bool {
		for _, e := range h.model.find("#room").items {
			if j, ok := e.(*irc.JoinEvent); ok && j.Agent == "guest" {
				return true
			}
		}
		return false
	})
	h.command("/kick guest quiet")
	h.until("kick", func() bool {
		for _, e := range h.model.find("#room").items {
			if k, ok := e.(*irc.KickEvent); ok && k.Agent == "guest" && k.Channel == "#room" {
				return true
			}
		}
		return false
	})
	if !c.Connected() {
		t.Fatal("channel kick disconnected client")
	}
	h.command("/deop helper")
	h.until("revoke", func() bool { s, _ := h.model.activeStatus(); return strings.Contains(s, "-o helper") })
}
