package protocol

import (
	"fmt"
	"testing"
)

func TestMentions(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"hello world", "[]"},
		{"@Anvil please look", "[anvil]"},
		{"please look @anvil, and (@trinode) then @anvil again", "[anvil trinode]"},
		{"anvil: do the thing", "[anvil]"},
		{"intro line\nKestrel: second paragraph\n- Note: after a bullet is not an address", "[kestrel]"},
		{"mail me at user@example.com or x@@y", "[]"},
		{"`@code` and @real.", "[code real]"},
		{"@anvil- is trimmed", "[anvil]"},
		{"@9lives starts with a digit", "[]"},
		{"time 12:30 is not an address", "[]"},
		{"Status: green @builder", "[builder status]"},
		{"@reviewer please check\n```go\n@fake annotation\nFake: example\n```\n@real thanks", "[reviewer real]"},
		{"````text\n```\n@fake\n```\n````\n@real", "[real]"},
		{"~~~text\n@fake\n~~~\nReal: please check", "[real]"},
	}
	for _, c := range cases {
		if got := fmt.Sprint(Mentions(c.body)); got != c.want {
			t.Errorf("Mentions(%q) = %s, want %s", c.body, got, c.want)
		}
	}
	if !MentionsNick("hey @ANVIL", "anvil") || MentionsNick("hey @anvilx", "anvil") {
		t.Error("MentionsNick must match whole names case-insensitively")
	}
}
