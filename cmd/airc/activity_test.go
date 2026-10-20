package main

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestOneShotAgentsShowAsActive(t *testing.T) {
	address := chatServer(t, 32)
	mustCLI(t, address, "check", "--nick", "builder", "--channel", "work", "--json")
	send(t, address, "builder", "#ops", "deploying")

	agents := cliJSON[[]irc.AgentInfo](t, address, "agents")
	if len(agents) != 1 || agents[0].Nick != "builder" || strings.Join(agents[0].Channels, ",") != "#ops,#work" || agents[0].ActiveAt.IsZero() || !agents[0].ConnectedAt.IsZero() {
		t.Fatalf("agents %+v", agents)
	}
	if out := mustCLI(t, address, "agents"); !strings.HasPrefix(out, "builder\t#ops,#work\tactive ") {
		t.Fatalf("agents reads %q", out)
	}
	if out := mustCLI(t, address, "directory", "--who", "builder"); !strings.Contains(out, "builder: unknown; active #ops,#work; last seen ") {
		t.Fatalf("directory reads %q", out)
	}
	if out := mustCLI(t, address, "send", "--nick", "planner", "--to", "builder", "--message", "can you review?"); !strings.Contains(out, "(queued: builder is at work and reads it on its next check)") {
		t.Fatalf("send reads %q", out)
	}
	if out := mustCLI(t, address, "send", "--nick", "planner", "--to", "nobody", "--message", "hello?"); !strings.Contains(out, "(queued: nobody can read it with airc check)") {
		t.Fatalf("send to someone idle reads %q", out)
	}
}
