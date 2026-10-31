package protocol

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
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

// Group tags follow the nickname tag's rules: the @ form only, whole words,
// case-insensitive, never inside fenced code or an address.
func TestGroupTags(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"hello world", "[]"},
		{"@humans the build is red", "[humans]"},
		{"(@Agents) and @HERE, then @humans and @agents again", "[agents here humans]"},
		{"@humanshere and @agentsx are names, @here- is here", "[here]"},
		{"humans: an address is not a group tag", "[]"},
		{"ops@here is an address, not a tag", "[]"},
		{"```\n@humans in code\n```\n@agents outside", "[agents]"},
		{"@anvil please, and @humans", "[humans]"},
	}
	for _, c := range cases {
		if got := fmt.Sprint(GroupTags(c.body)); got != c.want {
			t.Errorf("GroupTags(%q) = %s, want %s", c.body, got, c.want)
		}
	}
	for _, name := range []string{"humans", "Agents", "HERE"} {
		if !IsGroupTag(name) {
			t.Errorf("IsGroupTag(%q) = false", name)
		}
	}
	if IsGroupTag("human") || IsGroupTag("anvil") || IsGroupTag("") {
		t.Error("a nickname is not a group")
	}
	// The word stays a literal mention of a nickname of that name; the
	// server's resolution adds the group's members.
	if got := fmt.Sprint(Mentions("@humans @anvil")); got != "[humans anvil]" {
		t.Errorf("Mentions = %s", got)
	}
}

// A message's mentions are the names its body tags, who it is for and who
// its group tags resolved to, each once; a reader checks all three.
func TestMessageMentionsAndMentionedByIncludeResolvedGroups(t *testing.T) {
	body := "@humans and @anvil: the deploy failed"
	if got := fmt.Sprint(MessageMentions(body, "Trinode", []string{"sws", "anvil", "Kestrel"})); got != "[humans anvil trinode sws kestrel]" {
		t.Errorf("MessageMentions = %s", got)
	}
	if got := fmt.Sprint(MessageMentions(body, "", nil)); got != "[humans anvil]" {
		t.Errorf("MessageMentions without a for or groups = %s", got)
	}
	resolved := []string{"sws", "kestrel"}
	for nick, want := range map[string]bool{"anvil": true, "SWS": true, "kestrel": true, "trinode": false, "humans": true, "guest": false} {
		if got := MentionedBy(body, "", resolved, nick); got != want {
			t.Errorf("MentionedBy(%q) = %v, want %v", nick, got, want)
		}
	}
	if !MentionedBy("a step", "Trinode", nil, "trinode") || MentionedBy("@humans", "", nil, "sws") {
		t.Error("for counts; an unresolved group tag does not")
	}
	// MentionsNick stays literal for callers that match text alone.
	if MentionsNick("@humans please", "sws") || !MentionsNick("@humans please", "humans") {
		t.Error("MentionsNick must not resolve groups")
	}
}

// The patterns the scanners implement; tests keep them as the reference.
var (
	tagPattern     = regexp.MustCompile(`(?:^|[^A-Za-z0-9_@])@([A-Za-z_][A-Za-z0-9_-]{0,29})`)
	addressPattern = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_-]{0,29}):(?:\s|$)`)
)

func FuzzMentionScannersMatchPatterns(f *testing.F) {
	for _, seed := range []string{
		"@a@b", "x @a @b", "@a-@b", " @@a", "-@a", "é@a", "\xe2\x82@a", "\xe2\x82\x82\xac@a", "mail@example.com",
		"(@anvil), @anvil,", "@" + strings.Repeat("a", 35) + "@b", "nick: hi", "nick:", "nick:x", "nick :",
		strings.Repeat("a", 31) + ": hi", "_x:\tgo", "9lives: no", "a:\r", "@_", "@-", "@9",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, line string) {
		var want, got []string
		for _, m := range tagPattern.FindAllStringSubmatch(line, -1) {
			want = append(want, m[1])
		}
		lineTags(line, func(name string) { got = append(got, name) })
		if !slices.Equal(got, want) {
			t.Fatalf("%q: tags %q, pattern %q", line, got, want)
		}
		wantAddress := ""
		if m := addressPattern.FindStringSubmatch(line); m != nil {
			wantAddress = m[1]
		}
		if address := lineAddress(line); address != wantAddress && !strings.Contains(line, "\n") {
			t.Fatalf("%q: address %q, pattern %q", line, address, wantAddress)
		}
	})
}
