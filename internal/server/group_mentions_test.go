package server

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// profiled dials nick, joins rooms and gives it a profile kind, returning
// once the server has applied all of it.
func profiled(t *testing.T, address, nick, kind string, rooms ...string) *irc.Client {
	t.Helper()
	c := handoffClient(t, address, nick, rooms...)
	if kind != "" {
		if err := c.UpdateProfile(map[string]string{"kind": kind}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoffs"}); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// observing makes c an observer of targets, returning once the server has
// confirmed it (765); an observer is read-only from then on.
func observing(t *testing.T, c *irc.Client, targets ...string) {
	t.Helper()
	if err := c.Observe(targets...); err != nil {
		t.Fatal(err)
	}
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			if raw, ok := e.(*irc.RawEvent); ok && raw.Command == "765" {
				return
			}
		case <-timeout:
			t.Fatalf("observing %v was not confirmed", targets)
		}
	}
}

// A group tag is resolved when the message is posted: @humans and @agents
// against the directory's kinds, wherever those nicknames are, and @here
// against the room's live members, joined or observing, a guest included.
// The nicknames travel in the message's metadata and count as its mentions.
func TestGroupTagsResolveAgainstKindsAndRoomMembers(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, ReadTimeout: time.Hour}, nil)
	// kestrel is a human who is not connected when the messages are posted.
	profiled(t, address, "kestrel", "human").Close()
	sws := profiled(t, address, "sws", "human")
	observing(t, sws, "@sws")
	anvil := profiled(t, address, "anvil", "agent", "#room")
	guest := profiled(t, address, "guest", "", "#room")
	watcher := profiled(t, address, "watcher", "")
	observing(t, watcher, "#room")

	// post sends body to #room from c and returns it as the room's members
	// and observer receive it, each with the same resolved group.
	post := func(c *irc.Client, body, want string) *irc.MessageEvent {
		t.Helper()
		if err := c.Send("#room", body); err != nil {
			t.Fatal(err)
		}
		var first *irc.MessageEvent
		for _, reader := range []*irc.Client{anvil, guest, watcher} {
			m := nextMessage(t, reader, "")
			if m.Message != body || fmt.Sprint(m.GroupMentions) != want {
				t.Fatalf("%s received %q reaching %v, want %q reaching %s", reader.Nick(), m.Message, m.GroupMentions, body, want)
			}
			first = m
		}
		return first
	}
	humans := post(anvil, "@humans the build is red", "[kestrel sws]")
	// sws is not in the room, but observes her inbox, which the tag feeds.
	if woke := nextMessage(t, sws, ""); woke.ID != humans.ID || !woke.Mentions("SWS") || woke.Mentions("anvil") {
		t.Fatalf("sws was woken by %+v", woke)
	}
	here := post(anvil, "@here deploying now", "[guest watcher]")
	agents := post(guest, "@agents please review", "[anvil]")
	// Fenced code is literal, a tag of nobody; so is a group word in a DM.
	post(guest, "```\n@humans @here\n```\nnot a tag", "[]")
	if err := guest.Send("anvil", "@humans in a direct message"); err != nil {
		t.Fatal(err)
	}
	dm := nextMessage(t, anvil, "")
	if dm.Target != "anvil" || dm.GroupMentions != nil {
		t.Fatalf("the DM arrived as %+v", dm)
	}

	// The inbox of a human who was not even connected has the message, as a
	// nickname tag's would; the room's observer has the @here one and the
	// agent the @agents one. A one-shot reads an inbox once its nick is free.
	anvil.Close()
	watcher.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	inbox := func(nick string, want ...string) {
		t.Helper()
		entries, err := irc.RequestCheck(ctx, oneShot(t, address, nick), irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "@" + nick, After: "*", Limit: 10}}, MaxMessages: 10}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, e := range entries {
			if e.Message != nil {
				ids = append(ids, e.Message.ID)
				if protocol.IsChannel(e.Message.Target) && !protocol.MentionedBy(e.Message.Message, e.Message.For, e.Message.GroupMentions, nick) {
					t.Errorf("the inbox entry %q does not say it mentions %s", e.Message.Message, nick)
				}
			}
		}
		if !slices.Equal(ids, want) {
			t.Fatalf("inbox of %s holds %v, want %v", nick, ids, want)
		}
	}
	inbox("kestrel", humans.ID)
	inbox("watcher", here.ID)
	inbox("anvil", agents.ID, dm.ID) // not its own @here
}

// A group resolves to the first nicknames in sorted order that fit the
// count and byte bounds, never the sender; a group nobody belongs to
// resolves to nobody and the message posts.
func TestGroupMentionsAreBoundedAndSorted(t *testing.T) {
	s := New(Config{})
	for i := range protocol.MaxGroupMentions + 8 {
		nick := fmt.Sprintf("human-%02d", i)
		s.directory[nick] = protocol.AgentCard{Nick: nick, AgentProfile: protocol.AgentProfile{Kind: "human"}}
	}
	m := Message{From: "Human-00", Target: "#room", Body: "@humans @agents"}
	s.resolveGroupMentionsLocked(&m)
	if len(m.GroupMentions) != protocol.MaxGroupMentions || !slices.IsSorted(m.GroupMentions) || m.GroupMentions[0] != "human-01" || m.GroupMentions[len(m.GroupMentions)-1] != fmt.Sprintf("human-%02d", protocol.MaxGroupMentions) {
		t.Fatalf("resolved %d: %v", len(m.GroupMentions), m.GroupMentions)
	}
	if mentions := s.history.add(m); fmt.Sprint(mentions[:2]) != "[humans agents]" || len(mentions) != 2+protocol.MaxGroupMentions {
		t.Fatalf("the window indexes %v", mentions)
	}
	for i := range 20 {
		nick := strings.Repeat("a", 29) + string(rune('a'+i))
		s.directory[nick] = protocol.AgentCard{Nick: nick, AgentProfile: protocol.AgentProfile{Kind: "agent"}}
	}
	long := Message{From: "x", Target: "#room", Body: "@agents"}
	s.resolveGroupMentionsLocked(&long)
	if n := len(long.GroupMentions); n != protocol.MaxGroupMentionBytes/33 || long.GroupMentions[0] != strings.Repeat("a", 30) {
		t.Fatalf("resolved %d long names: %v", n, long.GroupMentions)
	}
	for _, empty := range []Message{
		{From: "x", Target: "#room", Body: "@here with nobody connected"},
		{From: "x", Target: "sws", Body: "@humans in a DM"},
		{From: "x", Target: "#room", Body: "@humans", Reaction: "@humans"},
		{From: "x", Target: "#room", Body: "humans: the address form is not a group tag"},
		{From: "x", Target: "#room", Body: "no tag", ChatMetadata: protocol.ChatMetadata{GroupMentions: []string{"stale"}}},
	} {
		if s.resolveGroupMentionsLocked(&empty); empty.GroupMentions != nil {
			t.Errorf("%q resolved to %v", empty.Body, empty.GroupMentions)
		}
	}
}

// The largest room messages, with their metadata fields at their limits
// and the largest group, still fit a wire line, live and as a record.
func TestLargestGroupMentionLinesFit(t *testing.T) {
	var nicks []string
	for bytes := 0; bytes+33 <= protocol.MaxGroupMentionBytes; bytes += 33 {
		nicks = append(nicks, strings.Repeat("n", 29)+string(rune('a'+len(nicks))))
	}
	base := Message{ID: strings.Repeat("a", 32), Seq: 1 << 62, From: strings.Repeat("n", 30), Target: strings.Repeat("#", 64), Body: strings.Repeat("x\n", 2048), Timestamp: time.Now().UTC(),
		ReplyTo: strings.Repeat("b", 32), ThreadID: strings.Repeat("c", 32)}
	base.AccountID, base.RequestID, base.Run, base.GroupMentions = strings.Repeat("d", 32), strings.Repeat("r", 64), strings.Repeat("l", 64), nicks
	shapes := map[string]func(m *Message){
		"a corrected paste": func(m *Message) {
			m.Kind, m.Supersedes = "notice", strings.Repeat("e", 32)
			m.Paste = &protocol.PasteInfo{ID: strings.Repeat("9", 32), Name: strings.Repeat("f", protocol.MaxPasteNameBytes), Language: "typescript", Size: 1 << 20, Lines: 1 << 16}
		},
		"a handoff result": func(m *Message) {
			m.Kind, m.For, m.Link = "handoff-done", strings.Repeat("f", 30), "https://example.com/"+strings.Repeat("p", protocol.MaxLinkBytes-20)
		},
		"a decision": func(m *Message) {
			m.Kind, m.For = "decision", strings.Repeat("f", 30)
			for i := range maxDecisionOptions {
				m.Choices = append(m.Choices, strings.Repeat(string(rune('a'+i)), maxDecisionOption))
			}
		},
	}
	for name, shape := range shapes {
		m := base
		shape(&m)
		live := formatMessage(m, strings.Repeat("u", 32))
		if len(live) > protocol.MaxLineLength {
			t.Errorf("%s: the live line is %d bytes, over %d", name, len(live), protocol.MaxLineLength)
		}
		record := protocol.Format("server", "762", []string{m.From, m.Target, "queued"}, encodeMessage(m))
		if !strings.HasSuffix(record, "\r\n") || len(record) > protocol.MaxLineLength {
			t.Errorf("%s: the receipt is %d bytes, over %d", name, len(record), protocol.MaxLineLength)
		}
		t.Logf("%s: live %d bytes, record %d bytes", name, len(live), len(record))
	}
}
