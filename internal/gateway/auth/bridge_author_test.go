package auth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/protocol"
)

// bridgeFixtureConfig is a principal-free gateway configuration with one
// mapped account token, for bridge sessions.
func bridgeFixtureConfig(t *testing.T) (config.Config, string) {
	t.Helper()
	root := privateTemp(t)
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, "fixture", filepath.Join(root, "state")
	c.CursorKeyFile = filepath.Join(root, "cursor.key")
	if err := CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(root, "carol.token")
	if err := CreateCredential(token); err != nil {
		t.Fatal(err)
	}
	return c, token
}

func TestGuestNickIsDerivedOnlyFromTheVerifiedIdentifier(t *testing.T) {
	for _, tc := range []struct{ short, external, want string }{
		{"alice", "alice", "irc-alice"},
		{"Alice", "Alice", "irc-alice"}, // services accounts fold case
		{"a_b-9", "a_b-9", "irc-a_b-9"},
	} {
		if got := GuestNick("irc-", tc.short, tc.external); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	// Bytes outside the plain alphabet, or overlong names, take a hash of
	// the whole identifier after '|', which no plain nickname contains.
	dotted := GuestNick("irc-", "a.b", "a.b")
	if !strings.HasPrefix(dotted, "irc-a-b|") || dotted == GuestNick("irc-", "a-b", "a-b") || dotted == GuestNick("irc-", "a+b", "a+b") {
		t.Fatal("lossy name collides", dotted)
	}
	long := GuestNick("irc-", strings.Repeat("a", 64), strings.Repeat("a", 64))
	if len(long) != 30 || !strings.Contains(long, "|") || long == GuestNick("irc-", strings.Repeat("a", 65), strings.Repeat("a", 65)) {
		t.Fatal("long name", long)
	}
	seen := map[string]string{}
	for i := range 20000 {
		external := []string{fmt.Sprintf("u%d", i), fmt.Sprintf("u.%d", i), fmt.Sprintf("U%d@x.org", i), fmt.Sprintf("@u%d:matrix.org", i), fmt.Sprintf("%s%d", strings.Repeat("z", i%40), i)}[i%5]
		nick := GuestNick("mx-", external, external)
		if !protocol.ValidNick(nick) || len(nick) > 30 || nick != GuestNick("mx-", external, external) {
			t.Fatal("invalid or unstable nick", external, nick)
		}
		key := strings.ToLower(nick)
		if other, ok := seen[key]; ok && !strings.EqualFold(other, external) {
			t.Fatal("collision", other, external, nick)
		}
		seen[key] = external
	}
}

func TestBridgeAuthorsMapGuestsAndSources(t *testing.T) {
	_, token := bridgeFixtureConfig(t)
	accounts := []config.BridgeAccount{{External: "carol@example.org", Nick: "carol", AccountTokenFile: token}}
	b, err := NewBridgeAuthors("xmpp-main", "xmpp-", "example.org", true, accounts)
	if err != nil {
		t.Fatal(err)
	}
	mapped, ok := b.Resolve("Carol@Example.org")
	if !ok || mapped.Author != (core.Author{Nick: "carol", Registered: true}) || mapped.Credential.Empty() || mapped.Source != "bridge:xmpp-main:carol" {
		t.Fatal("mapped account", mapped, ok)
	}
	local, ok := b.Resolve("alice@example.org")
	if !ok || local.Author != (core.Author{Nick: "xmpp-alice"}) || !local.Credential.Empty() || local.Source != "bridge:xmpp-main:xmpp-alice" {
		t.Fatal("local guest", local, ok)
	}
	foreign, ok := b.Resolve("alice@elsewhere.net")
	if !ok || foreign.Author.Registered || !strings.HasPrefix(foreign.Author.Nick, "xmpp-alice-elsewh") || !strings.Contains(foreign.Author.Nick, "|") || !protocol.ValidSource(foreign.Source) || foreign.Source == local.Source {
		t.Fatal("foreign guest", foreign, ok)
	}
	// A display name or nickname is never an input: the same JID is the same
	// author whatever the occupant calls itself.
	again, _ := b.Resolve("alice@example.org")
	if again.Author != local.Author || again.Source != local.Source {
		t.Fatal("unstable author")
	}
	closed, err := NewBridgeAuthors("xmpp-main", "xmpp-", "example.org", false, accounts)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := closed.Resolve("alice@example.org"); ok {
		t.Fatal("guest admitted with guests off")
	}
	if a, ok := closed.Resolve("carol@example.org"); !ok || !a.Author.Registered {
		t.Fatal("mapped account refused with guests off")
	}
	if closed.Fingerprint() == b.Fingerprint() {
		t.Fatal("guest policy not in fingerprint")
	}
	m, err := NewBridgeAuthors("matrix", "mx-", "example.org", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := m.Resolve("@alice:example.org"); a.Author.Nick != "mx-alice" || a.Source != "bridge:matrix:mx-alice" {
		t.Fatal("matrix local", a)
	}
	if a, _ := m.Resolve("@alice:other.org"); !strings.HasPrefix(a.Author.Nick, "mx-alice-other-org|") {
		t.Fatal("matrix foreign", a)
	}
	long, err := NewBridgeAuthors(strings.Repeat("n", 64), "irc-", "", true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a, _ := long.Resolve("alice"); !protocol.ValidSource(a.Source) || len(a.Source) > protocol.MaxSourceLength {
		t.Fatal("long bridge source", a.Source)
	}
	if err := os.WriteFile(token, []byte("short\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewBridgeAuthors("xmpp-main", "xmpp-", "example.org", true, accounts); err != ErrCredential {
		t.Fatal("bad token file accepted", err)
	}
}

func TestBridgeTextIsASendOrAnExplicitRead(t *testing.T) {
	target := core.Target{Kind: "room", Name: "#Allowed"}
	route := bridgeRoute{target: target, operations: []core.Operation{core.Send, core.HistoryRead}}
	author := BridgeAuthor{Author: core.Author{Nick: "irc-alice"}, Source: "bridge:n:irc-alice"}
	until := time.Now().Add(time.Minute)
	id := strings.Repeat("a", 64)
	send := newBridgeEvent(author, route, "hello AIRC-READ/1 world", id, until)
	if r := send.Request(); !send.Allowed() || send.IsRead() || r.Operation != core.Send || r.Message != "hello AIRC-READ/1 world" || *r.Target != target || r.RequestID != id {
		t.Fatal("plain send", r)
	}
	read := newBridgeEvent(author, route, `AIRC-READ/1 {"operation":"history.read","limit":5,"correlation_id":"c-1"}`, id, until)
	if r := read.Request(); !read.Allowed() || !read.IsRead() || r.Operation != core.HistoryRead || r.Limit != 5 || read.CorrelationID() != "c-1" || *r.Target != target {
		t.Fatal("read", r)
	}
	if bare := newBridgeEvent(author, route, "AIRC-READ/1", id, until); !bare.IsRead() || bare.Refused() || bare.Request().Operation != core.HistoryRead || bare.Request().Limit != DefaultBridgeReadLimit {
		t.Fatal("bare marker")
	}
	search := newBridgeEvent(author, route, `AIRC-READ/1 {"operation":"search","query":"x"}`, id, until)
	if !search.IsRead() || search.Refused() || search.Allowed() {
		t.Fatal("unrouted operation allowed")
	}
	for _, body := range []string{
		`AIRC-READ/1 {"operation":"send","message":"x"}`,
		`AIRC-READ/1 {"operation":"history.read","target":{"kind":"room","name":"#other"}}`,
		`AIRC-READ/1 {"operation":"history.read","unknown":1}`,
		`AIRC-READ/1 not json`,
		`AIRC-READ/1 {"operation":"history.read","correlation_id":"bad id"}`,
	} {
		e := newBridgeEvent(author, route, body, id, until)
		if !e.Valid() || !e.Refused() || !e.IsRead() || e.Allowed() {
			t.Fatal("malformed read not refused", body)
		}
	}
	if glued := newBridgeEvent(author, route, "AIRC-READ/1x", id, until); glued.IsRead() || glued.Request().Operation != core.Send {
		t.Fatal("marker prefix of a word")
	}
	var zero BridgeEvent
	if zero.Valid() || zero.Allowed() || zero.IsRead() {
		t.Fatal("zero event")
	}
}
