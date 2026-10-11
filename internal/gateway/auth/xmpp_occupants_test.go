//go:build unix

package auth

import (
	"context"
	"strconv"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

func rosterPresence(t *testing.T, nick, jid, kind string) *xmppwire.Element {
	t.Helper()
	e, err := xmppwire.Parse([]byte(`<presence xmlns="jabber:client" from="room@muc.localhost/` + nick + `" type="` + kind + `"><x xmlns="http://jabber.org/protocol/muc#user"><item jid="` + jid + `" role="participant" affiliation="member"/></x></presence>`))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestXMPPInitialRosterAndIdentityReplacement(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_roster")
	x, err := s.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err = x.JoinRooms(); err != nil {
		t.Fatal(err)
	}
	if !x.joined["room@muc.localhost"] || x.occupants["room@muc.localhost/alice"] != "alice@localhost/Phone" {
		t.Fatal("initial verified roster missing")
	}
	// Any real JID is retained (it becomes a guest or mapped author); the
	// occupant nickname never carries the old identity forward.
	if err = x.trackOccupant(rosterPresence(t, "alice", "other@localhost/Device", ""), "room@muc.localhost"); err != nil || x.occupants["room@muc.localhost/alice"] != "other@localhost/Device" {
		t.Fatal("nickname retained old identity", err)
	}
	if err = x.trackOccupant(rosterPresence(t, "alice", "bridge@localhost/Other", ""), "room@muc.localhost"); err != nil || len(x.occupants) != 0 {
		t.Fatal("bridge account echo retained", err)
	}
	if err = x.trackOccupant(rosterPresence(t, "newnick", "alice@localhost/Other", ""), "room@muc.localhost"); err != nil {
		t.Fatal(err)
	}
	if err = x.trackOccupant(rosterPresence(t, "newnick", "", "unavailable"), "room@muc.localhost"); err != nil || len(x.occupants) != 0 {
		t.Fatal("departure retained identity", err)
	}
	if err = x.trackOccupant(rosterPresence(t, "newnick", "", ""), "room@muc.localhost"); err == nil {
		t.Fatal("anonymous author retained")
	}
}

func TestXMPPRosterCapacityAndExistingRefresh(t *testing.T) {
	s, _ := xmppSessionFixture(t, "join_roster")
	x, err := s.Connect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer x.Close()
	if err = x.JoinRooms(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxXMPPOccupants-1; i++ {
		if err = x.trackOccupant(rosterPresence(t, "n"+strconv.Itoa(i), "alice@localhost/Device", ""), "room@muc.localhost"); err != nil {
			t.Fatal(i, err)
		}
	}
	if err = x.trackOccupant(rosterPresence(t, "overflow", "alice@localhost/Device", ""), "room@muc.localhost"); err == nil {
		t.Fatal("roster cap exceeded")
	}
	if err = x.trackOccupant(rosterPresence(t, "alice", "alice@localhost/New", ""), "room@muc.localhost"); err != nil {
		t.Fatal("existing identity could not refresh at capacity")
	}
	if err = x.trackOccupant(rosterPresence(t, "alice", "", "unavailable"), "room@muc.localhost"); err != nil {
		t.Fatal(err)
	}
	if err = x.trackOccupant(rosterPresence(t, "overflow", "alice@localhost/Device", ""), "room@muc.localhost"); err != nil {
		t.Fatal("departure did not release capacity")
	}
}
