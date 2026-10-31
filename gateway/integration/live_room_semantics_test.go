//go:build darwin || linux

package gateway_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestEventsWatchNativeRoomChangesAndSignalsKeepMessageCheckpoint(t *testing.T) {
	t.Parallel()
	f := newReactionTransportFixtureWithAdmin(t, true)
	author := dial(t, f.socket, "fixture-author", fixtureAccount, false, false)
	if err := author.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(event *irc.JoinEvent) bool { return event.Agent == author.Nick() })
	if err := author.SendWithID("#gateway-fixture", "retained root", "live-semantics-root"); err != nil {
		t.Fatal(err)
	}
	root := next(t, author, func(event *irc.MessageEvent) bool { return event.RequestID == "live-semantics-root" })
	poll := liveSemanticChat(t, author, irc.ChatRequest{Action: "poll", Target: "#gateway-fixture", Text: strings.Repeat("q", 1000), Options: []string{"yes", "no"}, Seconds: 60}, "results")
	native := liveSignalObserver(t, f.socket)
	r := core.Request{Version: 1, RequestID: "live-room-semantics", Operation: core.EventsWatch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Limit: 64, Start: "retained"}
	response, reader := liveDetachedStream(t, f, r, true)
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
	initial, checkpoint := ssePage(t, reader)
	if len(initial.Messages) != 2 || initial.Messages[0].ID != root.ID || initial.Messages[1].ID != poll.ID || checkpoint == "" || checkpoint != initial.Cursor {
		t.Fatalf("wrong initial message snapshot: %+v", initial)
	}
	peer := dial(t, f.socket, "live-peer", strings.Repeat("b", 64), true, false)
	if err := peer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	wantJoin := next(t, native, func(event *irc.JoinEvent) bool { return event.Agent == peer.Nick() })
	if got := liveSemanticEvent(t, reader, "join", checkpoint); got.Member != wantJoin.Agent {
		t.Fatal(got, wantJoin)
	}
	for _, topic := range []string{"updated topic ☃", ""} {
		if err := author.SetTopic("#gateway-fixture", topic); err != nil {
			t.Fatal(err)
		}
		want := next(t, native, func(event *irc.TopicEvent) bool { return event.SetBy == author.Nick() })
		got := liveSemanticEvent(t, reader, "topic", checkpoint)
		if got.Topic == nil || *got.Topic != want.Topic || got.Actor != want.SetBy {
			t.Fatal(got, want)
		}
	}
	admin := dial(t, f.socket, "live-control", "", false, true)
	token, err := auth.ReadToken(filepath.Join(filepath.Dir(f.socket), "admin.token"))
	if err != nil {
		t.Fatal(err)
	}
	if err := admin.AuthenticateAdmin(token); err != nil {
		t.Fatal(err)
	}
	next(t, admin, func(event *irc.RawEvent) bool { return event.Command == "381" })
	for _, mode := range []string{"+o", "-o"} {
		if err := admin.Raw("MODE #gateway-fixture " + mode + " live-peer"); err != nil {
			t.Fatal(err)
		}
		want := next(t, native, func(event *irc.RawEvent) bool { return event.Command == "MODE" })
		got := liveSemanticEvent(t, reader, "mode", checkpoint)
		if len(want.Params) != 3 || got.Mode != want.Params[1] || got.Member != want.Params[2] || got.Actor != admin.Nick() {
			t.Fatal(got, want)
		}
	}
	liveSemanticChat(t, author, liveRequest("typing"), "typing")
	liveSemanticSignal(t, native, reader, "typing", checkpoint)
	liveSemanticChat(t, peer, liveRequest("thinking"), "thinking")
	liveSemanticSignal(t, native, reader, "thinking", checkpoint)
	liveSemanticChat(t, author, irc.ChatRequest{Action: "prepare", ID: root.ID, Seconds: 20, Text: "reply coming"}, "prepare")
	liveSemanticSignal(t, native, reader, "prepare", checkpoint)
	liveSemanticChat(t, author, irc.ChatRequest{Action: "cancel", ID: root.ID}, "cancel")
	liveSemanticSignal(t, native, reader, "cancel", checkpoint)
	liveSemanticChat(t, peer, irc.ChatRequest{Action: "vote", ID: poll.ID, Choice: 1}, "results")
	if got := liveSemanticSignal(t, native, reader, "results", checkpoint); got.ID != poll.ID || got.Votes[0] != 1 || got.Closed {
		t.Fatal(got)
	}
	liveSemanticChat(t, author, irc.ChatRequest{Action: "close-poll", ID: poll.ID}, "results")
	if got := liveSemanticSignal(t, native, reader, "results", checkpoint); !got.Closed {
		t.Fatal(got)
	}
	reason := strings.Repeat("x", 500) + "\tbye"
	if err := peer.Part("#gateway-fixture", reason); err != nil {
		t.Fatal(err)
	}
	wantPart := next(t, native, func(event *irc.PartEvent) bool { return event.Agent == peer.Nick() })
	if got := liveSemanticEvent(t, reader, "part", checkpoint); got.Member != peer.Nick() || got.Reason != wantPart.Reason || got.Reason != reason {
		t.Fatal(got, wantPart)
	}
	if err := peer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, native, func(event *irc.JoinEvent) bool { return event.Agent == peer.Nick() })
	liveSemanticEvent(t, reader, "join", checkpoint)
	if err := admin.Raw("KICK #gateway-fixture live-peer :quiet"); err != nil {
		t.Fatal(err)
	}
	wantKick := next(t, native, func(event *irc.KickEvent) bool { return event.Agent == peer.Nick() })
	if got := liveSemanticEvent(t, reader, "kick", checkpoint); got.Member != wantKick.Agent || got.Actor != wantKick.By || got.Reason != wantKick.Reason {
		t.Fatal(got, wantKick)
	}
	quitter := dial(t, f.socket, "live-quitter", "", false, false)
	if err := quitter.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, native, func(event *irc.JoinEvent) bool { return event.Agent == quitter.Nick() })
	liveSemanticEvent(t, reader, "join", checkpoint)
	if err := quitter.Close(); err != nil {
		t.Fatal(err)
	}
	wantQuit := next(t, native, func(event *irc.QuitEvent) bool { return event.Agent == quitter.Nick() })
	if got := liveSemanticEvent(t, reader, "quit", checkpoint); got.Member != wantQuit.Agent || got.Reason != wantQuit.Reason {
		t.Fatal(got, wantQuit)
	}
	if err := author.SendWithID("#gateway-fixture", "after live events", "live-semantics-after"); err != nil {
		t.Fatal(err)
	}
	wantMessage := next(t, native, func(event *irc.MessageEvent) bool { return event.RequestID == "live-semantics-after" })
	page, id := ssePage(t, reader)
	if len(page.Messages) != 1 || len(page.Events) != 0 || page.Messages[0].ID != wantMessage.ID || page.Messages[0].Message != wantMessage.Message || id == checkpoint || id != page.Cursor {
		t.Fatalf("live message did not advance independently: %+v", page)
	}
}
