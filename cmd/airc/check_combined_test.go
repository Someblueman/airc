package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestCombinedCheckFallsBackForLargeValidSelectors(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	var rooms []string
	for i := range 63 {
		rooms = append(rooms, "#"+strings.Repeat("\\", 59)+fmt.Sprintf("%03d", i))
	}
	out := mustCLI(t, address, "check", "--nick", "reader", "--channel", strings.Join(rooms, ","), "--json")
	if len(checkBodies(t, out)) != 0 || checkFooter(t, out).Code != "no_messages" {
		t.Fatal(out)
	}
}
