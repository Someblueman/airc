package server

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// handoffClient dials nick and joins rooms, returning once the joins have
// taken effect; the test reads its replies with handoffChat and its
// deliveries with nextMessage.
func handoffClient(t *testing.T, address, nick string, rooms ...string) *irc.Client {
	t.Helper()
	c, err := irc.Dial(irc.Config{Nick: nick, Addr: address})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	for _, room := range rooms {
		if err := c.Join(room); err != nil {
			t.Fatal(err)
		}
	}
	// The server takes a connection's commands in order, so once a later
	// request is answered the joins have taken effect.
	if _, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoffs"}); err != nil {
		t.Fatal(err)
	}
	return c
}

// handoffChat sends r and returns the handoffs answered, and the messages
// delivered to c meanwhile.
func handoffChat(t *testing.T, c *irc.Client, r protocol.ChatRequest) ([]irc.ChatEntry, []*irc.MessageEvent, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var seen []*irc.MessageEvent
	entries, err := irc.RequestChat(ctx, c, r, func(e irc.Event) {
		if m, ok := e.(*irc.MessageEvent); ok {
			seen = append(seen, m)
		}
	})
	return entries, seen, err
}

func mustHandoff(t *testing.T, c *irc.Client, r protocol.ChatRequest) irc.ChatEntry {
	t.Helper()
	entries, _, err := handoffChat(t, c, r)
	if err != nil {
		t.Fatalf("%s: %v", r.Action, err)
	}
	if len(entries) != 1 || entries[0].Action != "handoff" {
		t.Fatalf("%s answered %+v", r.Action, entries)
	}
	return entries[0]
}

// nextMessage waits for c to be delivered a message of kind.
func nextMessage(t *testing.T, c *irc.Client, kind string) *irc.MessageEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case e := <-c.Events():
			if m, ok := e.(*irc.MessageEvent); ok && m.Kind == kind {
				return m
			}
		case <-timeout:
			t.Fatalf("no %s message", kind)
		}
	}
}

func TestRoomHandoffGoesToWhoeverAcceptsFirstAndEveryoneSeesItDone(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")

	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "finish task 7 on branch work-7"})
	if offer.State != "open" || offer.Holder != "" || offer.Message == nil || offer.Message.Kind != "handoff" || offer.Message.Message != "finish task 7 on branch work-7" {
		t.Fatalf("offer %+v", offer)
	}
	if m := nextMessage(t, reviewer, "handoff"); m.ID != offer.ID || m.Target != "#work" {
		t.Fatalf("the room saw %+v", m)
	}
	if _, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "you offered") {
		t.Fatal("the author accepted their own handoff:", err)
	}

	accepted := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID, Text: "on it"})
	if accepted.State != "accepted" || accepted.Holder != "builder" {
		t.Fatalf("accept %+v", accepted)
	}
	_, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	if err == nil || !strings.Contains(err.Error(), "already accepted by builder") {
		t.Fatal("a second taker was not told who holds it:", err)
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "only builder") {
		t.Fatal("someone else marked it done:", err)
	}
	m := nextMessage(t, planner, "handoff-accept")
	if m.From != "builder" || m.ReplyTo != offer.ID || m.Message != "on it" || m.For != "planner" {
		t.Fatalf("the author was told %+v", m)
	}

	done := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID, Text: "merged in #54"})
	if done.State != "done" || done.Holder != "builder" {
		t.Fatalf("done %+v", done)
	}
	for _, c := range []*irc.Client{planner, reviewer} {
		if m := nextMessage(t, c, "handoff-done"); m.ReplyTo != offer.ID || m.Message != "merged in #54" {
			t.Fatalf("done reached the room as %+v", m)
		}
	}
	if !slices.Contains(mentionsOf(srv, m.ID), "planner") {
		t.Fatal("the accept is not a mention of the author")
	}

	// Done is final; the room's listing keeps it, the default one does not.
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-decline", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "already done by builder") {
		t.Fatal("a finished handoff was declined:", err)
	}
	if room, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoffs", Target: "#work"}); err != nil || len(room) != 1 || room[0].State != "done" {
		t.Fatalf("room listing %+v: %v", room, err)
	}
	if mine, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoffs"}); err != nil || len(mine) != 0 {
		t.Fatalf("default listing kept a finished handoff %+v: %v", mine, err)
	}
}

func TestAddressedHandoffIsTheirsToTakeOrDecline(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")

	if _, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", For: "planner", Text: "x"}); err == nil {
		t.Fatal("handed off to oneself")
	}
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", For: "builder", Text: "review the parser"})
	if offer.Message.For != "builder" {
		t.Fatalf("offer %+v", offer)
	}
	if !slices.Contains(mentionsOf(srv, offer.ID), "builder") {
		t.Fatal("the offer is not a mention of builder")
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "for builder") {
		t.Fatal("someone else took an addressed handoff:", err)
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-decline", ID: offer.ID}); err == nil {
		t.Fatal("someone else declined an addressed handoff")
	}
	if mine, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoffs"}); err != nil || len(mine) != 1 || mine[0].ID != offer.ID {
		t.Fatalf("builder's listing %+v: %v", mine, err)
	}
	if theirs, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoffs"}); err != nil || len(theirs) != 0 {
		t.Fatalf("an addressed handoff is listed for others %+v: %v", theirs, err)
	}
	declined := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-decline", ID: offer.ID, Text: "no time today"})
	if declined.State != "declined" {
		t.Fatalf("decline %+v", declined)
	}
	if m := nextMessage(t, planner, "handoff-decline"); m.Message != "no time today" {
		t.Fatalf("the author was told %+v", m)
	}
}

func TestRoomHandoffGivenBackIsOpenAgain(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "triage the flaky test"})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	if open := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-decline", ID: offer.ID, Text: "blocked on the build"}); open.State != "open" || open.Holder != "" {
		t.Fatalf("given back %+v", open)
	}
	if taken := mustHandoff(t, reviewer, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID}); taken.Holder != "reviewer" {
		t.Fatalf("retaken %+v", taken)
	}
	// A withdrawal tells the holder.
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-withdraw", ID: offer.ID}); err == nil {
		t.Fatal("someone other than the author withdrew it")
	}
	if gone := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff-withdraw", ID: offer.ID}); gone.State != "withdrawn" {
		t.Fatalf("withdraw %+v", gone)
	}
	if m := nextMessage(t, reviewer, "handoff-withdraw"); m.For != "reviewer" || m.Message != "Withdrew handoff "+offer.ID {
		t.Fatalf("the holder was told %+v", m)
	}
}

func TestDirectHandoffStepsGoBetweenThePair(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner")
	builder := handoffClient(t, address, "builder")
	if _, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoff", Target: "builder", For: "reviewer", Text: "x"}); err == nil {
		t.Fatal("a direct handoff named someone else")
	}
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "builder", Text: "pick up the release notes"})
	if offer.Target != "builder" || offer.Message.For != "builder" {
		t.Fatalf("offer %+v", offer)
	}
	if m := nextMessage(t, builder, "handoff"); m.Target != "builder" {
		t.Fatalf("builder got %+v", m)
	}
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	if m := nextMessage(t, planner, "handoff-accept"); m.Target != "planner" || m.From != "builder" {
		t.Fatalf("the accept reached planner as %+v", m)
	}
}

func TestRepeatedHandoffStepAnswersWithoutPostingAgain(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "port the fix"})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	srv.mu.Lock()
	before := srv.history.size
	srv.mu.Unlock()
	if again := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID}); again.Holder != "builder" {
		t.Fatalf("repeat %+v", again)
	}
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID})
	srv.mu.Lock()
	after := srv.history.size
	srv.mu.Unlock()
	if after != before+1 {
		t.Fatalf("repeats posted %d messages, want only the one done", after-before)
	}
}

func TestHandoffsSurviveRestartsAndAgeOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "handoffs.jsonl")
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cfg := Config{HistoryLimit: 64, PingInterval: time.Hour, Now: clock}
	restore := func(s *Server) error { return s.RestoreHandoffs(path) }

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := New(cfg)
	if err := restore(first); err != nil {
		t.Fatal(err)
	}
	listener := listen(t)
	served := make(chan error, 1)
	go func() { served <- first.Serve(listener) }()
	address := listener.Addr().String()
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	kept := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "still open"})
	finished := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "finished long ago"})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: kept.ID})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: finished.ID})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: finished.ID})
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	// A crash mid-append leaves a torn last line.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"offer":{"id":"torn`); err != nil {
		t.Fatal(err)
	}
	f.Close()

	mu.Lock()
	now = now.Add(handoffKeep + time.Hour)
	mu.Unlock()
	second := New(cfg)
	if err := restore(second); err != nil {
		t.Fatal(err)
	}
	if len(second.handoffs.all) != 1 || second.handoffs.all[kept.ID].State != handoffAccepted || second.handoffs.all[kept.ID].Holder != "builder" {
		t.Fatalf("restored %+v", second.handoffs.all)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(string(data), "\n"); lines != 1 || !strings.HasSuffix(string(data), "\n") {
		t.Fatalf("the log was not compacted to one whole line:\n%s", data)
	}

	// A line that is not the last one is never skipped.
	if err := os.WriteFile(path, append([]byte("{}\n"), data...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(cfg).RestoreHandoffs(path); err == nil {
		t.Fatal("a corrupt handoff log was accepted")
	}
}

func TestHandoffLogIsRewrittenAfterAFailedAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.jsonl")
	s := New(Config{HistoryLimit: 8})
	if err := s.RestoreHandoffs(path); err != nil {
		t.Fatal(err)
	}
	actor := &session{client: Client{Nick: "planner"}, out: make(chan string, 64), done: make(chan struct{}), ephemeral: true}
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.offerHandoffLocked(actor, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "one"}); err != nil {
		t.Fatal(err)
	}
	// The append fails, as on a full disk, after writing part of a line.
	if _, err := s.handoffs.log.file.WriteString(`{"partial`); err != nil {
		t.Fatal(err)
	}
	_ = s.handoffs.log.file.Close()
	if err := s.offerHandoffLocked(actor, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "two"}); err == nil {
		t.Fatal("an append to a closed log succeeded")
	}
	if err := s.offerHandoffLocked(actor, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "three"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := handoffLedger.read(path)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("after a failed append the log holds %d handoffs: %v", len(loaded), err)
	}
}

// mentionsOf is who the retained message id counts as mentioning.
func mentionsOf(srv *Server, id string) []string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if entry := srv.history.entryByID(id); entry != nil {
		return entry.mentions
	}
	return nil
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

// Takers racing for a room handoff, with posts waiting on history syncs:
// exactly one holds it, and every other is told who does.
func TestConcurrentAcceptsHaveOneWinner(t *testing.T) {
	dir := t.TempDir()
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, func(s *Server) error {
		for _, err := range []error{s.RestoreHistory(filepath.Join(dir, "history")), s.RestoreHandoffs(filepath.Join(dir, "handoffs"))} {
			if err != nil {
				return err
			}
		}
		return nil
	})
	planner := handoffClient(t, address, "planner", "#work")
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "one of you, please"})
	takers := make([]*irc.Client, 8)
	for i := range takers {
		takers[i] = handoffClient(t, address, "taker"+string(rune('a'+i)), "#work")
	}
	type outcome struct {
		holder string
		err    error
	}
	results := make(chan outcome, len(takers))
	var start sync.WaitGroup
	start.Add(1)
	for _, c := range takers {
		go func() {
			start.Wait()
			entries, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
			if err == nil && len(entries) == 1 {
				results <- outcome{holder: entries[0].Holder}
				return
			}
			results <- outcome{err: err}
		}()
	}
	start.Done()
	var winners []string
	for range takers {
		r := <-results
		switch {
		case r.err == nil:
			winners = append(winners, r.holder)
		case !strings.Contains(r.err.Error(), "already accepted by "):
			t.Fatal(r.err)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("%d takers won: %v", len(winners), winners)
	}
}

// A one-shot agent joins no room, so the rooms it recently used are its
// rooms: open handoffs there are in its default listing.
func TestOneShotAgentListsOpenHandoffsInRoomsItUses(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "anyone: triage the flaky test"})
	for nick, room := range map[string]string{"builder": "#work", "stranger": "#elsewhere"} {
		c, err := irc.Dial(irc.Config{Nick: nick, Addr: address, Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if _, err := irc.RequestCheck(t.Context(), c, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room, Limit: 10}}, MaxMessages: 10}, nil); err != nil {
			t.Fatal(err)
		}
		listed, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoffs"})
		if err != nil {
			t.Fatal(err)
		}
		if want := nick == "builder"; (len(listed) == 1 && listed[0].ID == offer.ID) != want || len(listed) > 1 {
			t.Fatalf("%s, who checked %s, lists %+v", nick, room, listed)
		}
	}
}

// A holder marks a handoff blocked with a reason, which tags the author and
// is readable without knowing handoffs, takes it up again with accept, and
// finishes it with a link to the result.
func TestBlockedHandoffResumesAndFinishesWithALink(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")
	offer := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "port the fix to the release branch"})
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-blocked", ID: offer.ID, Text: "not yet"}); err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatal("an open handoff was blocked:", err)
	}
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	nextMessage(t, planner, "handoff-accept")
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-blocked", ID: offer.ID, Text: "CI is red"}); err == nil || !strings.Contains(err.Error(), "only builder") {
		t.Fatal("someone other than the holder blocked it:", err)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-blocked", ID: offer.ID, Text: " \n"}); err == nil || !strings.Contains(err.Error(), "say why") {
		t.Fatal("a blocked step without a reason was accepted:", err)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID, Link: "https://example.com/pr/1"}); err == nil || !strings.Contains(err.Error(), "a link goes with done") {
		t.Fatal("a link rode along on an accept:", err)
	}

	blocked := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-blocked", ID: offer.ID, Text: "waiting on the CI fix in #91"})
	if blocked.State != "blocked" || blocked.Holder != "builder" || blocked.BlockedReason != "waiting on the CI fix in #91" || blocked.BlockedAt.IsZero() {
		t.Fatalf("blocked %+v", blocked)
	}
	m := nextMessage(t, planner, "handoff-blocked")
	if m.From != "builder" || m.ReplyTo != offer.ID || m.For != "planner" || m.Message != "Blocked handoff "+offer.ID+": waiting on the CI fix in #91" {
		t.Fatalf("the author was told %+v", m)
	}
	if !slices.Contains(mentionsOf(srv, m.ID), "planner") {
		t.Fatal("the blocked step is not a mention of the author")
	}
	srv.mu.Lock()
	before := srv.history.size
	srv.mu.Unlock()
	if again := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-blocked", ID: offer.ID, Text: "still waiting"}); again.State != "blocked" || again.BlockedReason != blocked.BlockedReason {
		t.Fatalf("repeat %+v", again)
	}
	srv.mu.Lock()
	after := srv.history.size
	srv.mu.Unlock()
	if after != before {
		t.Fatal("a repeated blocked step posted again")
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "blocked by builder: waiting on the CI fix") {
		t.Fatal("someone else took a blocked handoff:", err)
	}
	if mine, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoffs"}); err != nil || len(mine) != 1 || mine[0].State != "blocked" || mine[0].BlockedReason == "" {
		t.Fatalf("the author's listing %+v: %v", mine, err)
	}

	// The holder's accept takes it up again.
	resumed := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: offer.ID})
	if resumed.State != "accepted" || resumed.Holder != "builder" || resumed.BlockedReason != "" || !resumed.BlockedAt.IsZero() {
		t.Fatalf("resumed %+v", resumed)
	}
	if m := nextMessage(t, planner, "handoff-accept"); m.Message != "Resumed handoff "+offer.ID || m.For != "planner" {
		t.Fatalf("the author was told %+v", m)
	}

	// Done records where the result is, after checking the link.
	// A 401-byte URL is one too long.
	for _, link := range []string{"/pr/54", "javascript:alert(1)", "https://example.com/" + strings.Repeat("x", protocol.MaxLinkBytes-19), "https://exa mple.com/", "ftp://example.com/x"} {
		if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID, Link: link}); err == nil || !strings.Contains(err.Error(), "absolute http(s) URL") {
			t.Fatalf("link %q was accepted: %v", link, err)
		}
	}
	link := "https://github.com/o/r/pull/54"
	done := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-done", ID: offer.ID, Link: link})
	if done.State != "done" || done.Link != link || done.Holder != "builder" {
		t.Fatalf("done %+v", done)
	}
	if m := nextMessage(t, planner, "handoff-done"); m.Message != "Finished handoff "+offer.ID+": "+link || m.Link != link || m.For != "planner" {
		t.Fatalf("the room saw %+v", m)
	}
	if room, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "handoffs", Target: "#work"}); err != nil || len(room) != 1 || room[0].Link != link || room[0].State != "done" {
		t.Fatalf("room listing %+v: %v", room, err)
	}
}

// A blocked handoff is restored as blocked, with its reason, and the
// caller's listing names blocked handoffs before the rest.
func TestBlockedHandoffsSurviveRestartsAndLeadTheListing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "handoffs.jsonl")
	cfg := Config{HistoryLimit: 64, PingInterval: time.Hour}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := New(cfg)
	if err := first.RestoreHandoffs(path); err != nil {
		t.Fatal(err)
	}
	listener := listen(t)
	served := make(chan error, 1)
	go func() { served <- first.Serve(listener) }()
	address := listener.Addr().String()
	planner := handoffClient(t, address, "planner", "#work")
	builder := handoffClient(t, address, "builder", "#work")
	moving := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "moving along"})
	stuck := mustHandoff(t, planner, protocol.ChatRequest{Action: "handoff", Target: "#work", Text: "stuck"})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: moving.ID})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-accept", ID: stuck.ID})
	mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-blocked", ID: stuck.ID, Text: "needs a credential"})
	for _, c := range []*irc.Client{planner, builder} {
		mine, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "handoffs"})
		if err != nil || len(mine) != 2 || mine[0].ID != stuck.ID || mine[0].State != "blocked" || mine[1].ID != moving.ID {
			t.Fatalf("%s's listing %+v: %v", c.Nick(), mine, err)
		}
	}
	// The room's listing keeps the offers' order.
	if room, _, err := handoffChat(t, planner, protocol.ChatRequest{Action: "handoffs", Target: "#work"}); err != nil || len(room) != 2 || room[0].ID != moving.ID {
		t.Fatalf("room listing %+v: %v", room, err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}

	second := New(cfg)
	if err := second.RestoreHandoffs(path); err != nil {
		t.Fatal(err)
	}
	restored := second.handoffs.all[stuck.ID]
	if restored.State != handoffBlocked || restored.Holder != "builder" || restored.BlockedReason != "needs a credential" || restored.BlockedAt.IsZero() {
		t.Fatalf("restored %+v", restored)
	}
	listener = listen(t)
	go func() { served <- second.Serve(listener) }()
	address = listener.Addr().String()
	builder = handoffClient(t, address, "builder", "#work")
	// Given back from blocked, it is open again with no reason left on it.
	if open := mustHandoff(t, builder, protocol.ChatRequest{Action: "handoff-decline", ID: stuck.ID}); open.State != "open" || open.Holder != "" || open.BlockedReason != "" || !open.BlockedAt.IsZero() {
		t.Fatalf("given back %+v", open)
	}
	if err := second.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	loaded, err := handoffLedger.read(path)
	if err != nil || loaded[stuck.ID].State != handoffOpen || loaded[stuck.ID].BlockedReason != "" {
		t.Fatalf("saved %+v: %v", loaded[stuck.ID], err)
	}
}

// The ledger logs are read as the daemon has always written them: one JSON
// object per line, the newest line for an offer winning, and compaction
// rewrites each record in the same shape.
func TestSavedLedgerLinesLoadUnchanged(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	cfg := Config{HistoryLimit: 8, Now: func() time.Time { return now }}
	offer := `"offer":{"account_id":"acct-1","kind":"handoff","for":"builder","id":"0123456789abcdef0123456789abcdef","seq":1,"from":"planner","target":"#work","message":"ship it","timestamp":"2026-10-01T09:00:00Z"}`
	open := `{` + offer + `,"state":"open","updated":"2026-10-01T09:00:00Z"}` + "\n"
	accepted := `{` + offer + `,"state":"accepted","holder":"builder","holder_account":"acct-2","updated":"2026-10-01T09:05:00Z"}` + "\n"
	handoffs := filepath.Join(dir, "handoffs.jsonl")
	if err := os.WriteFile(handoffs, []byte(open+accepted), 0o600); err != nil {
		t.Fatal(err)
	}
	s := New(cfg)
	if err := s.RestoreHandoffs(handoffs); err != nil {
		t.Fatal(err)
	}
	h := s.handoffs.all["0123456789abcdef0123456789abcdef"]
	if len(s.handoffs.all) != 1 || h.State != handoffAccepted || h.Holder != "builder" || h.HolderAccount != "acct-2" || h.Offer.For != "builder" || h.Offer.Body != "ship it" || h.Offer.AccountID != "acct-1" {
		t.Fatalf("loaded %+v", h)
	}
	if data, err := os.ReadFile(handoffs); err != nil || string(data) != accepted {
		t.Fatalf("compacted to %q, %v", data, err)
	}

	ask := `"offer":{"account_id":"acct-1","kind":"decision","for":"sws","choices":["a","b"],"id":"fedcba9876543210fedcba9876543210","seq":2,"from":"builder","target":"#work","message":"which?","timestamp":"2026-10-01T09:10:00Z"}`
	answer := `"answer":{"account_id":"acct-3","kind":"decision-answer","for":"builder","choice":2,"id":"00000000000000000000000000000001","reply_to":"fedcba9876543210fedcba9876543210","thread_id":"fedcba9876543210fedcba9876543210","seq":3,"from":"sws","target":"#work","message":"2. b","timestamp":"2026-10-01T09:20:00Z"}`
	asked := `{` + ask + `,"state":"open","updated":"2026-10-01T09:10:00Z"}` + "\n"
	answered := `{` + ask + `,"state":"answered",` + answer + `,"updated":"2026-10-01T09:20:00Z"}` + "\n"
	decisions := filepath.Join(dir, "decisions.jsonl")
	if err := os.WriteFile(decisions, []byte(asked+answered), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.RestoreDecisions(decisions); err != nil {
		t.Fatal(err)
	}
	d := s.decisions.all["fedcba9876543210fedcba9876543210"]
	if len(s.decisions.all) != 1 || d.State != decisionAnswered || d.Answer == nil || d.Answer.Choice != 2 || d.Answer.From != "sws" || strings.Join(d.Offer.Choices, "|") != "a|b" {
		t.Fatalf("loaded %+v", d)
	}
	if data, err := os.ReadFile(decisions); err != nil || string(data) != answered {
		t.Fatalf("compacted to %q, %v", data, err)
	}
}
