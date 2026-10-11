//go:build unix

package auth

import (
	"crypto/tls"
	"io"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

func xmppMessageScript(conn *tls.Conn, to, mode string) {
	room, _, _ := xmppwire.FullJID(to)
	message := func(nick, id string) string {
		return `<message type="groupchat" from="` + room + `/` + nick + `"><body>  café &amp; 世界  </body><stanza-id xmlns="urn:xmpp:sid:0" by="` + room + `" id="` + id + `"/></message>`
	}
	presence := func(jid string) string {
		return `<presence from="` + room + `/alice"><x xmlns="http://jabber.org/protocol/muc#user"><item jid="` + jid + `" role="participant" affiliation="member"/></x></presence>`
	}
	wire := message("alice", "opaque-ID")
	switch mode {
	case "join_message_guest":
		wire = presence("other@example.net/Device") + wire
	case "join_message_mapped":
		wire = presence("carol@localhost/Device") + wire
	case "join_message_delayed":
		wire = strings.Replace(wire, "<body>", `<delay xmlns="urn:xmpp:delay" stamp="2026-01-01T00:00:00Z"/><body>`, 1)
	case "join_message_read":
		wire = strings.Replace(wire, "  café &amp; 世界  ", `AIRC-READ/1 {&quot;operation&quot;:&quot;history.read&quot;,&quot;limit&quot;:4}`, 1)
	case "join_message_loop":
		wire = presence("bridge@localhost/airc") + wire
	case "join_message_budget":
		wire = strings.Repeat(presence("alice@localhost/Phone"), 128) + wire
	case "join_message_self":
		wire = `<presence from="` + to + `" type="unavailable"><x xmlns="http://jabber.org/protocol/muc#user"><item role="none" affiliation="member"/><status code="110"/></x></presence>` + wire
	case "join_message_rename":
		wire += strings.Replace(presence("alice@localhost/Phone"), `/alice"`, `/newnick"`, 1) + message("newnick", "opaque-ID")
	}
	_, _ = io.WriteString(conn, wire)
}
