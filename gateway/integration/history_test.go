package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHistoryBackendRoomPagesGap(t *testing.T) {
	withoutHistoryFile(t) // an evicted cursor must be a gap, not an archive read
	// This test reads live retention pages; native persistence is tested elsewhere.
	socket := nativeFixture(t, false, "none")
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, socket, "fixture-author", token, true, true)
	c, _ := acceptanceJournalFixture(t, socket, token)
	identity := fixtureIdentity(t, c)
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	post := func(id, body, parent string) *irc.SendReceiptEvent {
		t.Helper()
		var err error
		if parent == "" {
			err = author.SendWithID("#gateway-fixture", body, id)
		} else {
			err = author.ReplyWithID(parent, body, id)
		}
		if err != nil {
			t.Fatal(err)
		}
		return next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Message == body })
	}
	first := post("history-first", "whole\nmultiline history", "")
	second := post("history-second", "second page", first.ID)
	r := core.Request{Version: 1, RequestID: "room-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Cursor: "*", Limit: 1}
	read := func(request core.Request) backend.HistoryPage {
		t.Helper()
		g, err := s.AuthorizeAuthorRead(context.Background(), identity, request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		page, err := b.Read(context.Background(), g, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.Read(context.Background(), g, nil); !errors.Is(err, store.ErrNotReady) {
			t.Fatal("read lease reused", err)
		}
		return page
	}
	page := read(r)
	if len(page.Messages) != 1 || page.Messages[0].ID != first.ID || page.Messages[0].Message != first.Message || page.Messages[0].From != "fixture-author" || page.Messages[0].AccountID == "" || page.Status != "more" || page.Cursor != first.ID || page.Gap {
		t.Fatal("first page mismatch", page)
	}
	rootAccount := page.Messages[0].AccountID
	r.Cursor = page.Cursor
	page = read(r)
	if len(page.Messages) != 1 || page.Messages[0].ID != second.ID || page.Messages[0].Message != second.Message || page.Messages[0].ReplyTo != first.ID || page.Messages[0].ThreadID != first.ID || page.Messages[0].From != "fixture-author" || page.Messages[0].AccountID != rootAccount || page.Status != "ok" || page.Cursor != second.ID || page.Gap {
		t.Fatal("second page mismatch", page)
	}
	r.Cursor = page.Cursor
	page = read(r)
	if len(page.Messages) != 0 || page.Status != "ok" || page.Cursor != second.ID {
		t.Fatal("empty caught-up page mismatch", page)
	}
	for i := range 33 {
		post(fmt.Sprintf("evict-%d", i), fmt.Sprintf("eviction message %d", i), "")
	}
	r.Cursor = first.ID
	page = read(r)
	if !page.Gap || page.Status != "more" || len(page.Messages) != 1 {
		t.Fatal("retention gap hidden", page)
	}
}
