package backend

import (
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryCardValidationPreservesPublicProfileBoundaries(t *testing.T) {
	valid := irc.AgentCard{Nick: "agent", State: "unknown"}
	if !validCard(valid) {
		t.Fatal("valid unknown card rejected")
	}
	for _, change := range []func(*irc.AgentCard){
		func(c *irc.AgentCard) { c.State = "administrator" },
		func(c *irc.AgentCard) { c.AccountID = "forged-account" },
		func(c *irc.AgentCard) { c.About = strings.Repeat("x", 401) },
		func(c *irc.AgentCard) { c.Tools = "tools\ncontrol" },
		func(c *irc.AgentCard) { c.Note = strings.Repeat("x", 241) },
		func(c *irc.AgentCard) { c.Workspace = string([]byte{0xff}) },
	} {
		card := valid
		change(&card)
		if validCard(card) {
			t.Fatal("invalid card accepted", card)
		}
	}
}
