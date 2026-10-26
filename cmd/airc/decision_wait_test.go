package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCheckWaitsForADecision(t *testing.T) {
	tap := tapDaemon(t, chatServer(t, 64))
	address := tap.addr
	offer := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--channel", "work", "--from", "sws", "--message", "Ship?", "--option", "yes", "--option", "no")
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "builder", "--decision", offer.ID, "--wait", "10s", "--json")
		done <- result{out, err}
	}()
	tap.waitObserving(t, 1)
	// Traffic in the decision's room, and a reply to the offer that is not an
	// answer, do not end the wait.
	send(t, address, "reviewer", "#work", "@builder unrelated")
	replyMessage(t, address, "reviewer", offer.ID, "I would say yes")
	mustCLI(t, address, "decide", "answer", offer.ID, "--nick", "sws", "--choice", "1", "--message", "ship it")
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatal(got.err)
		}
		rows := jsonLines[decisionRow](t, got.out)
		if len(rows) != 2 || rows[0].Type != "decision" || rows[0].State != "answered" || rows[0].Choice != 1 || rows[0].AnsweredBy != "sws" || rows[0].Answer != "Decided 1 (yes): ship it" {
			t.Fatalf("wait result: %s", got.out)
		}
		if status := checkFooter(t, got.out); status.Code != "decided" {
			t.Fatalf("wait status: %s", got.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer did not wake the wait")
	}
	// Already answered, it returns at once; without --json, as text.
	start := time.Now()
	out := mustCLI(t, address, "check", "--nick", "builder", "--decision", offer.ID, "--wait", "5s")
	if !strings.HasPrefix(out, "decision "+offer.ID+" answered by sws: 1. yes in #work from builder\n") || time.Since(start) > 2*time.Second {
		t.Fatalf("answered decision = %q after %s", out, time.Since(start))
	}

	// A decision outlives the history ring: once the offer has been evicted,
	// the wait still ends with the answer, since it watches the room the
	// answer is posted to rather than the offer's thread.
	aged := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--channel", "work", "--from", "sws", "--message", "Still there?", "--option", "yes", "--option", "no")
	for i := range 70 {
		send(t, address, "reviewer", "#work", fmt.Sprintf("filler %d", i))
	}
	if out := mustCLI(t, address, "history", "#work", "--json"); strings.Contains(out, aged.ID) {
		t.Fatal("the offer was not evicted")
	}
	go func() {
		out, _, err := cli(t, address, "check", "--nick", "builder", "--decision", aged.ID, "--wait", "10s", "--json")
		done <- result{out, err}
	}()
	tap.waitObserving(t, 1)
	mustCLI(t, address, "decide", "answer", aged.ID, "--nick", "sws", "--choice", "2")
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("waiting on an evicted offer: %v", got.err)
		}
		if rows := jsonLines[decisionRow](t, got.out); len(rows) != 2 || rows[0].State != "answered" || rows[0].Choice != 2 || checkFooter(t, got.out).Code != "decided" {
			t.Fatalf("wait result: %s", got.out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the answer to an evicted offer did not wake the wait")
	}

	// An open decision outlasts a short wait quietly, and a wait of zero
	// reads the row as it stands.
	open := cliJSON[decisionRow](t, address, "decide", "--nick", "builder", "--to", "sws", "--message", "Tag it?")
	start = time.Now()
	out = mustCLI(t, address, "check", "--nick", "builder", "--decision", open.ID, "--wait", "300ms", "--json")
	rows := jsonLines[decisionRow](t, out)
	if len(rows) != 2 || rows[0].State != "open" || checkFooter(t, out).Code != "wait_expired" || time.Since(start) < 250*time.Millisecond {
		t.Fatalf("timeout = %q after %s", out, time.Since(start))
	}
	if out := mustCLI(t, address, "check", "--nick", "builder", "--decision", open.ID, "--json"); checkFooter(t, out).Code != "wait_expired" {
		t.Fatalf("no wait = %q", out)
	}
	for _, args := range [][]string{
		{"check", "--nick", "builder", "--decision", open.ID, "--channel", "work"},
		{"check", "--nick", "builder", "--decision", open.ID, "--mentions"},
		{"check", "--nick", "builder", "--decision", open.ID, "--reply-to", open.ID},
		{"check", "--nick", "builder", "--decision", open.ID, "--peek"},
		{"check", "--nick", "builder", "--decision", open.ID, "--compact"},
		{"check", "--nick", "builder", "--decision", "bad"},
	} {
		deniedCLI(t, address, "--decision requires a message ID and cannot be combined with", args...)
	}
	deniedCLI(t, address, "decision is not retained", "check", "--nick", "builder", "--decision", strings.Repeat("ab", 16))
}
