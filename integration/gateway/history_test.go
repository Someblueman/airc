package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestHistoryBackendRoomPagesGapAndRevocation(t *testing.T) {
	socket := fixture(t)
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	author := dial(t, socket, "fixture-author", token, true, true)
	c, _ := acceptanceJournalFixture(t, socket, token)
	c.Principals[0].Operations = append(c.Principals[0].Operations, core.HistoryRead)
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
	post := func(id, body string) *irc.SendReceiptEvent {
		t.Helper()
		if err := author.SendWithID("#gateway-fixture", body, id); err != nil {
			t.Fatal(err)
		}
		return next(t, author, func(e *irc.SendReceiptEvent) bool { return e.Message == body })
	}
	first := post("history-first", "whole\nmultiline history")
	second := post("history-second", "second page")
	r := core.Request{Version: 1, RequestID: "room-history", Operation: core.HistoryRead, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Cursor: "*", Limit: 1}
	read := func(request core.Request) backend.HistoryPage {
		t.Helper()
		g, err := s.AuthorizeHistory(context.Background(), identity, request, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		page, err := b.History(context.Background(), g)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := b.History(context.Background(), g); !errors.Is(err, store.ErrNotReady) {
			t.Fatal("read lease reused", err)
		}
		return page
	}
	page := read(r)
	if len(page.Messages) != 1 || page.Messages[0].ID != first.ID || page.Messages[0].Message != first.Message || page.Status != "more" || page.Cursor != first.ID || page.Gap {
		t.Fatal("first page mismatch", page)
	}
	r.Cursor = page.Cursor
	page = read(r)
	if len(page.Messages) != 1 || page.Messages[0].ID != second.ID || page.Status != "ok" || page.Cursor != second.ID || page.Gap {
		t.Fatal("second page mismatch", page)
	}
	r.Cursor = page.Cursor
	page = read(r)
	if len(page.Messages) != 0 || page.Status != "ok" || page.Cursor != second.ID {
		t.Fatal("empty caught-up page mismatch", page)
	}
	for i := range 33 {
		post(fmt.Sprintf("evict-%d", i), fmt.Sprintf("eviction message %d", i))
	}
	r.Cursor = first.ID
	page = read(r)
	if !page.Gap || page.Status != "more" || len(page.Messages) != 1 {
		t.Fatal("retention gap hidden", page)
	}
	foreign := r
	foreign.Target = &core.Target{Kind: "room", Name: "#private"}
	if _, err := s.AuthorizeHistory(context.Background(), identity, foreign, time.Now()); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("foreign room authorized", err)
	}
	g, err := s.AuthorizeHistory(context.Background(), identity, r, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(context.Background(), identity.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeHistory(context.Background(), identity, r, time.Now()); !errors.Is(err, auth.ErrForbidden) {
		t.Fatal("revoked history authorized", err)
	}
	if _, err := b.History(context.Background(), g); err != nil {
		t.Fatal("pre-revocation lease could not finish", err)
	}
}
