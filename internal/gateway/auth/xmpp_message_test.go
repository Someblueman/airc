//go:build unix

package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestXMPPVerifiedWholeMessageAndRoomScopedID(t *testing.T) {
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
	if !a.Valid() || a.Identity().ID() != "device" || a.Author() != "alice@localhost" || a.Room() != "room@muc.localhost" || a.Body() != "  café & 世界  " {
		t.Fatal("wrong proof", a)
	}
	ops := a.Operations()
	ops[0] = core.HistoryRead
	if a.Operations()[0] != core.Send {
		t.Fatal("mutable operations")
	}
	b, err := x.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if a.RequestID() == "" || a.RequestID() != b.RequestID() || a.Occupant() == b.Occupant() {
		t.Fatal("nickname changed stable identity")
	}
	other := a
	other.room = "other@muc.localhost"
	if other.RequestID() == a.RequestID() {
		t.Fatal("room issuer omitted")
	}
	if (VerifiedXMPPMessage{}).Valid() || (VerifiedXMPPMessage{}).RequestID() != "" {
		t.Fatal("zero proof grants authority")
	}
}

func TestXMPPMessageRefusalsAndSessionLoss(t *testing.T) {
	for _, mode := range []string{"unmapped", "loop", "budget", "self"} {
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
			reusable := mode == "unmapped" || mode == "loop"
			if errors.Is(err, ErrXMPPMessage) != reusable || x.usable() != reusable {
				t.Fatal("wrong session disposition", err)
			}
		})
	}
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
