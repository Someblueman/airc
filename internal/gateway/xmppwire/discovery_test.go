package xmppwire

import (
	"strings"
	"testing"
)

const roomDiscovery = `<iq xmlns="jabber:client" type="result" from="room@muc.example.com" to="bridge@example.com/airc" id="disco-1"><query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="http://jabber.org/protocol/muc"/><feature var="muc_nonanonymous"/><feature var="urn:xmpp:sid:0"/></query></iq>`

func TestXMPPExactRoomDiscovery(t *testing.T) {
	w, err := Discover("room@muc.example.com", "disco-1")
	if err != nil {
		t.Fatal(err)
	}
	if parsed(t, string(w)).Name.Local != "iq" {
		t.Fatal("incorrect query")
	}
	if !Discovered(parsed(t, roomDiscovery), "room@muc.example.com", "bridge@example.com/airc", "disco-1") {
		t.Fatal("selected room refused")
	}
	for _, raw := range []string{
		strings.Replace(roomDiscovery, "disco-1", "other", 1), strings.Replace(roomDiscovery, `type="result"`, `type="error"`, 1),
		strings.Replace(roomDiscovery, "room@muc.example.com", "other@muc.example.com", 1),
		strings.Replace(roomDiscovery, "bridge@example.com/airc", "other@example.com/airc", 1),
		strings.Replace(roomDiscovery, "muc_nonanonymous", "muc_semianonymous", 1),
		strings.Replace(roomDiscovery, "urn:xmpp:sid:0", "urn:fake", 1),
		strings.Replace(roomDiscovery, `category="conference"`, `category="client"`, 1),
		strings.Replace(roomDiscovery, `</query>`, `<feature var="muc_nonanonymous"/></query>`, 1),
		strings.Replace(roomDiscovery, `</query>`, `<feature var="muc_anonymous"/></query>`, 1),
		strings.Replace(roomDiscovery, `<feature var="urn:xmpp:sid:0"/>`, `<feature var="urn:xmpp:sid:0"><nested/></feature>`, 1),
	} {
		if Discovered(parsed(t, raw), "room@muc.example.com", "bridge@example.com/airc", "disco-1") {
			t.Fatal("false discovery accepted")
		}
	}
	if _, err := Discover("room@muc.example.com/nick", "id"); err == nil {
		t.Fatal("occupant discovery accepted")
	}
}
