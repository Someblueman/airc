package irc_test

import (
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDMAuditIncludesOfflineAndLiveMessagesWithoutRoomBroadcasts(t *testing.T) {
	address := startConfigured(t, server.Config{HistoryLimit: 64}, "")
	audit := dialOneShot(t, "human", address)
	if !audit.Supports("DM_AUDIT") {
		t.Fatal("missing audit capability")
	}
	if err := audit.Observe(irc.AllDirectMessages, "@muse"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		nextEvent(t, audit, func(e irc.Event) bool { raw, ok := e.(*irc.RawEvent); return ok && raw.Command == "765" })
	}
	room := dialOneShot(t, "roomreader", address)
	if err := room.Observe("#ops"); err != nil {
		t.Fatal(err)
	}
	nextEvent(t, room, func(e irc.Event) bool { raw, ok := e.(*irc.RawEvent); return ok && raw.Command == "765" })
	carol := newClient(t, "carol", address)
	queued := sendOneShot(t, "planner", address, "muse", "offline assignment")
	live := sendOneShot(t, "muse", address, "carol", "private reply")
	public := sendOneShot(t, "planner", address, "#ops", "public progress @muse")
	if !queued.Queued || live.Queued {
		t.Fatal("wrong offline/live receipt")
	}
	// Audit + recipient subscriptions overlap, but each DM is delivered once.
	var audited []string
	for i := 0; i < 3; i++ { // two DMs, plus the public mention from @muse
		message := nextEvent(t, audit, func(e irc.Event) bool { _, ok := e.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
		audited = append(audited, message.ID)
	}
	if fmt.Sprint(audited) != fmt.Sprint([]string{queued.ID, live.ID, public.ID}) {
		t.Fatalf("overlapping audit subscriptions repeated or missed a message: %v", audited)
	}
	roomMessage := nextEvent(t, room, func(e irc.Event) bool { _, ok := e.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if roomMessage.ID != public.ID {
		t.Fatalf("private message leaked to the room: %+v", roomMessage)
	}
	carolMessage := nextEvent(t, carol, func(e irc.Event) bool { _, ok := e.(*irc.MessageEvent); return ok }).(*irc.MessageEvent)
	if carolMessage.ID != live.ID {
		t.Fatalf("recipient missed its DM: %+v", carolMessage)
	}
	got, status := readHistory(t, audit, irc.AllDirectMessages, "*", 1)
	if status != "more" || len(got) != 1 || got[0].ID != queued.ID {
		t.Fatalf("first audit page = %v %s", bodies(got), status)
	}
	got, status = readHistory(t, audit, irc.AllDirectMessages, queued.ID, 1)
	if status != "ok" || len(got) != 1 || got[0].ID != live.ID {
		t.Fatalf("second audit page = %v %s", bodies(got), status)
	}
	if got, _ := readHistory(t, audit, "@muse", "*", 10); len(got) != 2 || got[0].ID != queued.ID || got[1].ID != public.ID {
		t.Fatalf("agent inbox included someone else's DM: %v", bodies(got))
	}
}
