package server

import (
	"context"
	"fmt"
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

func mustDecision(t *testing.T, c *irc.Client, r protocol.ChatRequest) irc.ChatEntry {
	t.Helper()
	entries, _, err := handoffChat(t, c, r)
	if err != nil {
		t.Fatalf("%s: %v", r.Action, err)
	}
	if len(entries) != 1 || entries[0].Action != "decision" {
		t.Fatalf("%s answered %+v", r.Action, entries)
	}
	return entries[0]
}

func TestRoomDecisionIsAnsweredByItsDeciderAndEveryoneSees(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	sws := handoffClient(t, address, "sws", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")
	if !sws.Supports("DECISION") {
		t.Fatal("the server does not advertise DECISION")
	}

	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Ship with the simple parser?", Options: []string{"yes", "no, rewrite it"}})
	if offer.State != "open" || offer.From != "builder" || offer.Answer != nil || offer.Message == nil || offer.Message.Kind != "decision" || offer.Message.For != "sws" || strings.Join(offer.Message.Choices, "|") != "yes|no, rewrite it" || offer.Profile == nil || offer.Profile.Nick != "builder" {
		t.Fatalf("offer %+v", offer)
	}
	if m := nextMessage(t, reviewer, "decision"); m.ID != offer.ID || m.Target != "#work" || len(m.Choices) != 2 || m.For != "sws" {
		t.Fatalf("the room saw %+v", m)
	}
	if !slices.Contains(mentionsOf(srv, offer.ID), "sws") {
		t.Fatal("the offer is not a mention of the decider")
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1}); err == nil || !strings.Contains(err.Error(), "for sws") {
		t.Fatal("a bystander answered:", err)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1}); err == nil || !strings.Contains(err.Error(), "for sws") {
		t.Fatal("the asker answered their own decision:", err)
	}
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 3}); err == nil || !strings.Contains(err.Error(), "1 to 2") {
		t.Fatal("an option out of range was accepted:", err)
	}
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "option number, a note, or both") {
		t.Fatal("an empty answer was accepted:", err)
	}

	answered := mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 2, Text: "the edge cases matter"})
	if answered.State != "answered" || answered.Answer == nil || answered.Answer.From != "sws" || answered.Answer.Choice != 2 || answered.Answer.ReplyTo != offer.ID || answered.Answer.Message != "Decided 2 (no, rewrite it): the edge cases matter" {
		t.Fatalf("answer %+v", answered)
	}
	for _, c := range []*irc.Client{builder, reviewer} {
		if m := nextMessage(t, c, "decision-answer"); m.ReplyTo != offer.ID || m.Choice != 2 || m.For != "builder" || m.From != "sws" || m.Message != answered.Answer.Message {
			t.Fatalf("the answer reached the room as %+v", m)
		}
	}
	if !slices.Contains(mentionsOf(srv, answered.Answer.ID), "builder") {
		t.Fatal("the answer is not a mention of the asker")
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1}); err == nil || !strings.Contains(err.Error(), "already answered by sws") {
		t.Fatal("an answered decision was answered again:", err)
	}
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "decision-withdraw", ID: offer.ID}); err == nil || !strings.Contains(err.Error(), "already answered by sws") {
		t.Fatal("an answered decision was withdrawn:", err)
	}
	// Answered is final; the room's listing keeps it, the default one does not.
	if room, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "decisions", Target: "#work"}); err != nil || len(room) != 1 || room[0].State != "answered" || room[0].Answer == nil {
		t.Fatalf("room listing %+v: %v", room, err)
	}
	if mine, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decisions"}); err != nil || len(mine) != 0 {
		t.Fatalf("default listing kept an answered decision %+v: %v", mine, err)
	}
	if one, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "decisions", ID: offer.ID}); err != nil || len(one) != 1 || one[0].ID != offer.ID {
		t.Fatalf("listing by id %+v: %v", one, err)
	}
}

func TestDecisionWithoutOptionsNeedsANoteAndRejectsAChoice(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	sws := handoffClient(t, address, "sws", "#work")
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Which name for the flag?"})
	if offer.Message.Choices != nil {
		t.Fatalf("offer %+v", offer)
	}
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1}); err == nil || !strings.Contains(err.Error(), "no options") {
		t.Fatal("a choice on a decision without options was accepted:", err)
	}
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Text: "  "}); err == nil || !strings.Contains(err.Error(), "answer with a note") {
		t.Fatal("an empty answer was accepted:", err)
	}
	answered := mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Text: "--decision-file"})
	if answered.Answer == nil || answered.Answer.Choice != 0 || answered.Answer.Message != "--decision-file" {
		t.Fatalf("answer %+v", answered)
	}
	if m := nextMessage(t, builder, "decision-answer"); m.Message != "--decision-file" || m.Choice != 0 {
		t.Fatalf("the asker was told %+v", m)
	}
}

func TestDecisionOptionLimits(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	many := make([]string, 9)
	for i := range many {
		many[i] = fmt.Sprint("option ", i)
	}
	for name, options := range map[string][]string{
		"one":       {"only"},
		"nine":      many,
		"long":      {"a", strings.Repeat("b", 81)},
		"duplicate": {"same", "same"},
		"empty":     {"a", " "},
	} {
		if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "x", Options: options}); err == nil {
			t.Fatalf("%s option list was accepted", name)
		}
	}
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "x", Options: []string{" a ", "b"}})
	if strings.Join(offer.Message.Choices, "|") != "a|b" {
		t.Fatalf("options were not trimmed: %+v", offer.Message.Choices)
	}
	for _, r := range []protocol.ChatRequest{
		{Action: "decision", Target: "#work", Text: "x"},                 // a room decision names its decider
		{Action: "decision", Target: "#work", For: "builder", Text: "x"}, // not oneself
		{Action: "decision", Target: "#work", For: "sws", Text: " "},     // a question
		{Action: "decisions", Target: "sws"},                             // rooms only
		{Action: "decisions", Limit: 1001},
	} {
		if _, _, err := handoffChat(t, builder, r); err == nil {
			t.Fatalf("%+v was accepted", r)
		}
	}
}

func TestRegisteredDeciderMustPresentItsAccount(t *testing.T) {
	dir := t.TempDir()
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, func(s *Server) error {
		return s.RestoreAccounts(filepath.Join(dir, "accounts.json"))
	})
	token := strings.Repeat("c", 64)
	account, err := irc.Dial(irc.Config{Nick: "sws", Addr: address, IdentityToken: token, CreateAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := account.Join("#work"); err != nil {
		t.Fatal(err)
	}
	builder := handoffClient(t, address, "builder", "#work")
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Merge?", Options: []string{"yes", "no"}})
	account.Close()
	// Nobody else can be sws: a guest cannot take the registered nickname.
	if _, err := irc.Dial(irc.Config{Nick: "sws", Addr: address}); err == nil {
		t.Fatal("a guest took a registered nickname")
	}
	other, err := irc.Dial(irc.Config{Nick: "someone", Addr: address, IdentityToken: strings.Repeat("d", 64), CreateAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, _, err := handoffChat(t, other, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1}); err == nil || !strings.Contains(err.Error(), "for sws") {
		t.Fatal("another account answered:", err)
	}
	// The account, back under its credential, answers; the answer carries it.
	again, err := irc.Dial(irc.Config{Nick: "sws", Addr: address, IdentityToken: token})
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if err := again.Join("#work"); err != nil {
		t.Fatal(err)
	}
	answered := mustDecision(t, again, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1})
	if answered.Answer == nil || answered.Answer.AccountID == "" || answered.Answer.From != "sws" {
		t.Fatalf("answer %+v", answered)
	}
}

func TestAdminMayAnswerOrWithdrawADecision(t *testing.T) {
	address, _ := adminWireServer(t)
	builder := handoffClient(t, address, "builder", "#work")
	sws := handoffClient(t, address, "sws", "#work")
	admin := handoffClient(t, address, "owner", "#work")
	if err := admin.AuthenticateAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	first := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Ship?", Options: []string{"yes", "no"}})
	answered := mustDecision(t, admin, protocol.ChatRequest{Action: "decision-answer", ID: first.ID, Choice: 1, Text: "sws is out today"})
	if answered.State != "answered" || answered.Answer.From != "owner" {
		t.Fatalf("the admin's answer is attributed to %+v", answered.Answer)
	}
	if m := nextMessage(t, sws, "decision-answer"); m.From != "owner" || m.For != "builder" {
		t.Fatalf("the room saw %+v", m)
	}
	second := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Tag it?"})
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-withdraw", ID: second.ID}); err == nil || !strings.Contains(err.Error(), "author or an admin") {
		t.Fatal("the decider withdrew the decision:", err)
	}
	if gone := mustDecision(t, admin, protocol.ChatRequest{Action: "decision-withdraw", ID: second.ID}); gone.State != "withdrawn" || gone.Answer != nil {
		t.Fatalf("withdraw %+v", gone)
	}
	if m := nextMessage(t, sws, "decision-withdraw"); m.From != "owner" || m.For != "sws" || m.Message != "Withdrew decision "+second.ID {
		t.Fatalf("the decider was told %+v", m)
	}
}

func TestDirectDecisionStepsGoBetweenThePair(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder")
	sws := handoffClient(t, address, "sws")
	if _, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "decision", Target: "sws", For: "reviewer", Text: "x"}); err == nil {
		t.Fatal("a direct decision named someone else")
	}
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "sws", Text: "Keep the old flag name?", Options: []string{"keep", "rename"}})
	if offer.Target != "sws" || offer.Message.For != "sws" {
		t.Fatalf("offer %+v", offer)
	}
	if m := nextMessage(t, sws, "decision"); m.Target != "sws" || m.From != "builder" {
		t.Fatalf("sws got %+v", m)
	}
	mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1})
	if m := nextMessage(t, builder, "decision-answer"); m.Target != "builder" || m.From != "sws" || m.Message != "Decided 1 (keep)" {
		t.Fatalf("the answer reached builder as %+v", m)
	}
	// Each side's default listing had it while open; the asker's shows the answer by id.
	if one, _, err := handoffChat(t, builder, protocol.ChatRequest{Action: "decisions", ID: offer.ID}); err != nil || len(one) != 1 || one[0].Answer == nil || one[0].Answer.Choice != 1 {
		t.Fatalf("listing %+v: %v", one, err)
	}
}

func TestDecisionWithdrawTellsTheDecider(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	sws := handoffClient(t, address, "sws", "#work")
	reviewer := handoffClient(t, address, "reviewer", "#work")
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Ship?"})
	if mine, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decisions"}); err != nil || len(mine) != 1 || mine[0].ID != offer.ID {
		t.Fatalf("the decider's listing %+v: %v", mine, err)
	}
	if _, _, err := handoffChat(t, reviewer, protocol.ChatRequest{Action: "decision-withdraw", ID: offer.ID}); err == nil {
		t.Fatal("someone other than the author withdrew it")
	}
	gone := mustDecision(t, builder, protocol.ChatRequest{Action: "decision-withdraw", ID: offer.ID, Text: "answered in the thread"})
	if gone.State != "withdrawn" {
		t.Fatalf("withdraw %+v", gone)
	}
	m := nextMessage(t, sws, "decision-withdraw")
	if m.For != "sws" || m.Message != "answered in the thread" || m.ReplyTo != offer.ID {
		t.Fatalf("the decider was told %+v", m)
	}
	if !slices.Contains(mentionsOf(srv, m.ID), "sws") {
		t.Fatal("the withdrawal is not a mention of the decider")
	}
	if _, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Text: "yes"}); err == nil || !strings.Contains(err.Error(), "already withdrawn") {
		t.Fatal("a withdrawn decision was answered:", err)
	}
	if mine, _, err := handoffChat(t, sws, protocol.ChatRequest{Action: "decisions"}); err != nil || len(mine) != 0 {
		t.Fatalf("a withdrawn decision is still listed %+v: %v", mine, err)
	}
}

func TestRepeatedDecisionStepAnswersWithoutPostingAgain(t *testing.T) {
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	sws := handoffClient(t, address, "sws", "#work")
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Ship?", Options: []string{"yes", "no"}})
	mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 1})
	srv.mu.Lock()
	before := srv.history.size
	srv.mu.Unlock()
	if again := mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: offer.ID, Choice: 2}); again.Answer == nil || again.Answer.Choice != 1 {
		t.Fatalf("repeat %+v", again)
	}
	other := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Tag?"})
	mustDecision(t, builder, protocol.ChatRequest{Action: "decision-withdraw", ID: other.ID})
	mustDecision(t, builder, protocol.ChatRequest{Action: "decision-withdraw", ID: other.ID})
	srv.mu.Lock()
	after := srv.history.size
	srv.mu.Unlock()
	if after != before+2 {
		t.Fatalf("repeats posted %d messages, want only the offer and its withdrawal", after-before)
	}
}

func TestDecisionsSurviveRestartsAndAgeOut(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions.jsonl")
	var mu sync.Mutex
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	cfg := Config{HistoryLimit: 64, PingInterval: time.Hour, Now: clock}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first := New(cfg)
	if err := first.RestoreAccounts(filepath.Join(dir, "accounts.json")); err != nil {
		t.Fatal(err)
	}
	if err := first.RestoreDecisions(path); err != nil {
		t.Fatal(err)
	}
	listener := listen(t)
	served := make(chan error, 1)
	go func() { served <- first.Serve(listener) }()
	address := listener.Addr().String()
	builder := handoffClient(t, address, "builder", "#work")
	sws, err := irc.Dial(irc.Config{Nick: "sws", Addr: address, IdentityToken: strings.Repeat("c", 64), CreateAccount: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sws.Close() })
	if err := sws.Join("#work"); err != nil {
		t.Fatal(err)
	}
	kept := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "still open", Options: []string{"a", "b"}})
	finished := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "answered long ago"})
	answered := mustDecision(t, sws, protocol.ChatRequest{Action: "decision-answer", ID: finished.ID, Text: "with the simple one"})
	if answered.Answer.AccountID == "" {
		t.Fatalf("the answer carries no account: %+v", answered.Answer)
	}
	// Answered a moment ago, it survives one restart with its answer intact.
	if err := first.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-served; err != nil {
		t.Fatal(err)
	}
	restored := New(cfg)
	if err := restored.RestoreDecisions(path); err != nil {
		t.Fatal(err)
	}
	if d := restored.decisions[finished.ID]; len(restored.decisions) != 2 || d.State != decisionAnswered || d.Answer == nil || d.Answer.From != "sws" || d.Answer.AccountID != answered.Answer.AccountID || d.Answer.Body != "with the simple one" {
		t.Fatalf("restored %+v", restored.decisions)
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
	now = now.Add(decisionKeep + time.Hour)
	mu.Unlock()
	second := New(cfg)
	if err := second.RestoreDecisions(path); err != nil {
		t.Fatal(err)
	}
	if d := second.decisions[kept.ID]; len(second.decisions) != 1 || d.State != decisionOpen || strings.Join(d.Offer.Choices, "|") != "a|b" {
		t.Fatalf("restored %+v", second.decisions)
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
	if err := New(cfg).RestoreDecisions(path); err == nil {
		t.Fatal("a corrupt decision log was accepted")
	}
}

func TestDecisionLogIsRewrittenAfterAFailedAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	s := New(Config{HistoryLimit: 8})
	if err := s.RestoreDecisions(path); err != nil {
		t.Fatal(err)
	}
	actor := &session{client: Client{Nick: "builder"}, out: make(chan string, 64), done: make(chan struct{}), ephemeral: true}
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	ask := func(text string) error {
		return s.offerDecisionLocked(actor, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: text})
	}
	if err := ask("one"); err != nil {
		t.Fatal(err)
	}
	// The append fails, as on a full disk, after writing part of a line.
	if _, err := s.decisionLog.file.WriteString(`{"partial`); err != nil {
		t.Fatal(err)
	}
	_ = s.decisionLog.file.Close()
	if err := ask("two"); err == nil {
		t.Fatal("an append to a closed log succeeded")
	}
	if err := ask("three"); err != nil {
		t.Fatal(err)
	}
	loaded, err := readDecisions(path)
	if err != nil || len(loaded) != 2 {
		t.Fatalf("after a failed append the log holds %d decisions: %v", len(loaded), err)
	}
}

// A one-shot agent joins no room, so the rooms it recently used are its
// rooms: open decisions asked there are in its default listing, and so is
// what it asked itself.
func TestOneShotAgentListsDecisionsInRoomsItUses(t *testing.T) {
	address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour}, nil)
	builder := handoffClient(t, address, "builder", "#work")
	offer := mustDecision(t, builder, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "Ship?"})
	for nick, room := range map[string]string{"reviewer": "#work", "stranger": "#elsewhere"} {
		c, err := irc.Dial(irc.Config{Nick: nick, Addr: address, Ephemeral: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		if _, err := irc.RequestCheck(t.Context(), c, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room, Limit: 10}}, MaxMessages: 10}, nil); err != nil {
			t.Fatal(err)
		}
		listed, _, err := handoffChat(t, c, protocol.ChatRequest{Action: "decisions"})
		if err != nil {
			t.Fatal(err)
		}
		if want := nick == "reviewer"; (len(listed) == 1 && listed[0].ID == offer.ID) != want || len(listed) > 1 {
			t.Fatalf("%s, who checked %s, lists %+v", nick, room, listed)
		}
	}
	asker, err := irc.Dial(irc.Config{Nick: "builder2", Addr: address, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer asker.Close()
	direct := mustDecision(t, asker, protocol.ChatRequest{Action: "decision", Target: "sws", Text: "Rename it?"})
	if mine, _, err := handoffChat(t, asker, protocol.ChatRequest{Action: "decisions"}); err != nil || len(mine) != 1 || mine[0].ID != direct.ID {
		t.Fatalf("the asker's listing %+v: %v", mine, err)
	}
}

func TestTooManyOpenDecisionsAreRefused(t *testing.T) {
	s := New(Config{HistoryLimit: 8})
	actor := &session{client: Client{Nick: "builder"}, out: make(chan string, 64), done: make(chan struct{}), ephemeral: true}
	s.messageMu.Lock()
	defer s.messageMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	for range maxLiveDecisions {
		m := s.newMessage("builder", "#work", "question", nil)
		m.Kind, m.For = "decision", "sws"
		s.decisions[m.ID] = decision{Offer: m, State: decisionOpen, Updated: m.Timestamp}
	}
	err := s.offerDecisionLocked(actor, protocol.ChatRequest{Action: "decision", Target: "#work", For: "sws", Text: "one more"})
	if err == nil || !strings.Contains(err.Error(), "10000 decisions are open") {
		t.Fatal("the cap was not enforced:", err)
	}
}
