package main

import (
	"os"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestLoginAndChannelOperatorCLI(t *testing.T) {
	address := chatServer(t, 64)
	mustCLI(t, address, "user", "create", "--nick", "alice")
	if output := mustCLI(t, address, "user", "login", "--nick", "alice"); !strings.Contains(output, "User alice ready") {
		t.Fatal(output)
	}
	deniedCLI(t, address, "no such file", "user", "login", "--nick", "missing")
	deniedCLI(t, address, "privileges", "op", "--nick", "alice", "--channel", "#room", "--who", "alice")
	path, err := defaultAdminTokenFile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	mustCLI(t, address, "op", "--channel", "#room", "--who", "alice", "--token-file", path)
	if output := mustCLI(t, address, "operators", "--channel", "#room"); !strings.Contains(output, "alice") {
		t.Fatal(output)
	}
	mustCLI(t, address, "room", "#room", "--nick", "alice", "--as-operator", "--slow", "1s")
	deniedCLI(t, address, "requires admin or channel", "room", "#other", "--nick", "alice", "--as-operator", "--slow", "1s")
	victim, err := irc.Dial(irc.Config{Addr: address, Nick: "guest"})
	if err != nil {
		t.Fatal(err)
	}
	defer victim.Close()
	victim.Join("#room")
	for e := range victim.Events() {
		if _, ok := e.(*irc.JoinEvent); ok {
			break
		}
	}
	mustCLI(t, address, "kick", "--nick", "alice", "--channel", "#room", "--who", "guest")
	mustCLI(t, address, "deop", "--nick", "alice", "--channel", "#room", "--who", "alice")
	deniedCLI(t, address, "privileges", "kick", "--nick", "alice", "--channel", "#room", "--who", "guest")
	mustCLI(t, address, "away", "--nick", "alice", "--message", "back later", "--ttl", "5m")
	if output := mustCLI(t, address, "directory", "--who", "alice", "--json"); !strings.Contains(output, `"state":"away"`) {
		t.Fatal(output)
	}
}
