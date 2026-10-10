package xmppwire

import (
	"strings"
	"testing"
)

const groupMessage = `<message xmlns="jabber:client" type="groupchat" from="room@muc.example.com/nick" to="bridge@example.com/airc"><body>whole 世界 &amp; body  </body><origin-id xmlns="urn:xmpp:sid:0" id="untrusted"/><stanza-id xmlns="urn:xmpp:sid:0" by="room@muc.example.com" id="Opaque-ID"/></message>`

func TestXMPPWholeGroupMessageAndOpaqueRoomID(t *testing.T) {
	m, err := GroupMessage(parsed(t, groupMessage), "room@muc.example.com", "bridge@example.com/airc")
	if err != nil || m.Body != "whole 世界 & body  " || m.ID != "Opaque-ID" || m.Occupant != "room@muc.example.com/nick" {
		t.Fatal(m, err)
	}
	for _, raw := range []string{
		strings.Replace(groupMessage, `type="groupchat"`, `type="chat"`, 1),
		strings.Replace(groupMessage, "room@muc.example.com/nick", "other@muc.example.com/nick", 1),
		strings.Replace(groupMessage, "bridge@example.com/airc", "other@example.com/airc", 1),
		strings.Replace(groupMessage, `by="room@muc.example.com"`, `by="other@muc.example.com"`, 1),
		strings.Replace(groupMessage, `id="Opaque-ID"`, `id=""`, 1),
		strings.Replace(groupMessage, `</message>`, `<stanza-id xmlns="urn:xmpp:sid:0" by="room@muc.example.com" id="second"/></message>`, 1),
		strings.Replace(groupMessage, `</message>`, `<body>second</body></message>`, 1),
		strings.Replace(groupMessage, `whole 世界 &amp; body  `, `<nested/>`, 1),
		strings.Replace(groupMessage, `whole 世界 &amp; body  `, `   `, 1),
	} {
		if _, err := GroupMessage(parsed(t, raw), "room@muc.example.com", "bridge@example.com/airc"); err == nil {
			t.Fatal("ambiguous or foreign message accepted")
		}
	}
}
