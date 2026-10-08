package xmppwire

import (
	"strings"
	"testing"
)

const ownPresence = `<presence xmlns="jabber:client" from="room@muc.example.com/airc" to="bridge@example.com/airc"><x xmlns="http://jabber.org/protocol/muc#user"><item affiliation="member" role="participant"/><status code="100"/><status code="110"/></x></presence>`

func TestXMPPOwnNonAnonymousJoin(t *testing.T) {
	w, err := Join("room@muc.example.com", "airc")
	if err != nil {
		t.Fatal(err)
	}
	e := parsed(t, string(w))
	x, err := child(e, "jabber:client", "presence", muc, "x")
	if err != nil {
		t.Fatal(err)
	}
	h, err := child(x, muc, "x", muc, "history")
	if err != nil || len(h.Attributes) != 1 || h.Attributes[0].Value != "0" {
		t.Fatal("historical replay not disabled")
	}
	if !OwnJoined(parsed(t, ownPresence), "room@muc.example.com", "bridge@example.com", "airc") {
		t.Fatal("own join refused")
	}
	for _, raw := range []string{
		strings.Replace(ownPresence, `code="100"`, `code="170"`, 1),
		strings.Replace(ownPresence, `code="110"`, `code="210"`, 1),
		strings.Replace(ownPresence, `/airc" to=`, `/other" to=`, 1),
		strings.Replace(ownPresence, `from=`, `type="error" from=`, 1),
		strings.Replace(ownPresence, `role="participant"`, `role="visitor"`, 1),
		strings.Replace(ownPresence, `affiliation="member"`, `affiliation="outcast"`, 1),
		strings.Replace(ownPresence, `<item `, `<item jid="other@example.com/airc" `, 1),
		strings.Replace(ownPresence, `</x>`, `<status code="100"/></x>`, 1),
		strings.Replace(ownPresence, `</x>`, `<status code="201"/></x>`, 1),
		strings.Replace(ownPresence, `</x>`, `<item role="participant" affiliation="member"/></x>`, 1),
		strings.ReplaceAll(ownPresence, mucUser, "urn:fake"),
	} {
		if OwnJoined(parsed(t, raw), "room@muc.example.com", "bridge@example.com", "airc") {
			t.Fatal("false self/nonanonymous join accepted")
		}
	}
	if _, err := Join("room@muc.example.com/other", "airc"); err == nil {
		t.Fatal("multi-resource room accepted")
	}
}
