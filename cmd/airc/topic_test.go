package main

import (
	"strings"
	"testing"
)

func TestTopicCommandSetsReadsAndClears(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	if out := mustCLI(t, address, "topic", "hq"); !strings.Contains(out, "has no topic") { // bare name accepted
		t.Fatalf("empty topic = %q", out)
	}
	out := mustCLI(t, address, "topic", "#hq", "--nick", "planner", "--set", "Welcome! Tag @planner for decisions.")
	if !strings.Contains(out, "set the topic of #hq") {
		t.Fatalf("set = %q", out)
	}
	if got := strings.TrimSpace(mustCLI(t, address, "topic", "#hq")); got != "Welcome! Tag @planner for decisions." {
		t.Fatalf("read = %q", got)
	}
	if topic := cliJSON[struct{ Channel, Topic string }](t, address, "topic", "#hq"); topic.Topic == "" || topic.Channel != "#hq" {
		t.Fatalf("json = %+v", topic)
	}
	if _, _, err := cli(t, address, "topic", "#hq", "--set", strings.Repeat("x", 500), "--nick", "planner"); err == nil || !strings.Contains(err.Error(), "400 bytes") {
		t.Fatalf("an oversized topic should be refused clearly, got %v", err)
	}
	if _, _, err := cli(t, address, "topic", "#hq", "--set", "x", "--clear", "--nick", "planner"); err == nil {
		t.Fatal("--set and --clear together should be rejected")
	}
	if out := mustCLI(t, address, "topic", "#hq", "--clear", "--nick", "planner"); !strings.Contains(out, "cleared") {
		t.Fatalf("clear = %q", out)
	}
	if out := mustCLI(t, address, "topic", "#hq"); !strings.Contains(out, "has no topic") {
		t.Fatalf("after clear = %q", out)
	}
}
