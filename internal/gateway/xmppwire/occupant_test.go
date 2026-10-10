package xmppwire

import (
	"strings"
	"testing"
)

const occupantPresence = `<presence xmlns="jabber:client" from="room@muc.example.com/nick" to="bridge@example.com/airc"><x xmlns="http://jabber.org/protocol/muc#user"><item jid="alice@example.com/Device" role="participant" affiliation="member"/></x></presence>`

func TestXMPPOccupantArrivalAndDeparture(t *testing.T) {
	o, err := Presence(parsed(t, occupantPresence), "room@muc.example.com", "bridge@example.com/airc")
	if err != nil || o.Address != "room@muc.example.com/nick" || o.RealJID != "alice@example.com/Device" || o.Departed {
		t.Fatal(o, err)
	}
	departure := strings.Replace(occupantPresence, `<presence `, `<presence type="unavailable" `, 1)
	departure = strings.Replace(departure, `jid="alice@example.com/Device" `, `nick="replacement" `, 1)
	o, err = Presence(parsed(t, departure), "room@muc.example.com", "bridge@example.com/airc")
	if err != nil || !o.Departed || o.Address != "room@muc.example.com/nick" || o.RealJID != "" {
		t.Fatal(o, err)
	}
	for _, raw := range []string{
		strings.Replace(occupantPresence, `jid="alice@example.com/Device" `, "", 1),
		strings.Replace(occupantPresence, "alice@example.com/Device", "Alice@example.com/Device", 1),
		strings.Replace(occupantPresence, "room@muc.example.com/nick", "other@muc.example.com/nick", 1),
		strings.Replace(occupantPresence, "bridge@example.com/airc", "other@example.com/airc", 1),
		strings.Replace(occupantPresence, `<presence `, `<presence type="error" `, 1),
		strings.Replace(occupantPresence, `</x>`, `<item jid="other@example.com/r"/></x>`, 1),
		strings.Replace(occupantPresence, `</x>`, `<status code="173"/></x>`, 1),
		strings.Replace(occupantPresence, `</x>`, `<status code="110"/></x>`, 1),
	} {
		if _, err := Presence(parsed(t, raw), "room@muc.example.com", "bridge@example.com/airc"); err == nil {
			t.Fatal("ambiguous/anonymous occupant accepted")
		}
	}
}
