//go:build unix

package auth

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestXMPPPlainMessageAuthorFromRealJID(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_message_rename")
	x, err := s.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err = x.JoinRooms(); err != nil {
		t.Fatal(err)
	}
	a, err := x.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	e := a.Event()
	if !a.Valid() || e.Author().Author != (core.Author{Nick: "xmpp-alice"}) || e.Author().Source != "bridge:xmpp:xmpp-alice" || a.Author() != "alice@localhost" || a.Room() != "room@muc.localhost" ||
		e.Request().Operation != core.Send || e.Request().Message != "  café & 世界  " || e.Target().Name != "#Allowed" {
		t.Fatal("wrong proof", a)
	}
	b, err := x.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestID() == "" || a.RequestID() != b.RequestID() || a.Occupant() == b.Occupant() || b.Event().Author().Author != e.Author().Author {
		t.Fatal("nickname changed stable identity")
	}
	if (VerifiedXMPPMessage{}).Valid() || (VerifiedXMPPMessage{}).RequestID() != "" {
		t.Fatal("zero proof grants authority")
	}
}

func TestXMPPGuestMappedAndReadAuthors(t *testing.T) {
	for _, mode := range []string{"guest", "mapped", "read"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, "join_message_"+mode)
			x, err := s.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer x.Close()
			if err = x.JoinRooms(); err != nil {
				t.Fatal(err)
			}
			m, err := x.ReadMessage()
			e := m.Event()
			if err != nil || !m.Valid() {
				t.Fatal(err)
			}
			switch mode {
			case "guest":
				if a := e.Author().Author; a.Registered || !strings.HasPrefix(a.Nick, "xmpp-other-example-|") {
					t.Fatal("foreign guest", a)
				}
			case "mapped":
				if a := e.Author(); a.Author != (core.Author{Nick: "carol", Registered: true}) || a.Credential.Empty() {
					t.Fatal("mapped account", a)
				}
			case "read":
				if !e.IsRead() || !e.Allowed() || e.Request().Operation != core.HistoryRead || e.Request().Limit != 4 {
					t.Fatal("read", e.Request())
				}
			}
		})
	}
}

func TestXMPPMessageRefusalsAndSessionLoss(t *testing.T) {
	for _, mode := range []string{"loop", "delayed", "budget", "self"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, "join_message_"+mode)
			x, err := s.Connect(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer x.Close()
			if err = x.JoinRooms(); err != nil {
				t.Fatal(err)
			}
			m, err := x.ReadMessage()
			if err == nil || m.Valid() {
				t.Fatal("refusal yielded authority")
			}
			reusable := mode == "loop" || mode == "delayed"
			if errors.Is(err, ErrXMPPMessage) != reusable || x.usable() != reusable {
				t.Fatal("wrong session disposition", err)
			}
		})
	}
	t.Run("guests_off", func(t *testing.T) {
		s, _ := xmppSessionFixture(t, "join_message")
		s.authors.guests = false
		x, err := s.Connect(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer x.Close()
		if err = x.JoinRooms(); err != nil {
			t.Fatal(err)
		}
		if m, err := x.ReadMessage(); !errors.Is(err, ErrXMPPMessage) || m.Valid() || !x.usable() {
			t.Fatal("guest admitted with guests off", err)
		}
	})
}

func TestXMPPBufferedMessageRefusedAfterCancellation(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_message")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, err := s.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err = x.JoinRooms(); err != nil {
		t.Fatal(err)
	}
	cancel()
	if m, err := x.ReadMessage(); err == nil || m.Valid() {
		t.Fatal("cancelled message granted authority")
	}
	var absent *XMPPSession
	if m, err := absent.ReadMessage(); err == nil || m.Valid() {
		t.Fatal("nil session granted authority")
	}
}
