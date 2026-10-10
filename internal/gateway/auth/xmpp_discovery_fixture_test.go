//go:build unix

package auth

import (
	"crypto/tls"
	"encoding/xml"
	"io"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

func xmppDiscoveryScript(conn *tls.Conn, r *xmppwire.Stream, mode string) bool {
	request, err := r.Read()
	if err != nil {
		return false
	}
	attrs := map[string]string{}
	for _, a := range request.Attributes {
		if a.Name.Space == "" {
			attrs[a.Name.Local] = a.Value
		}
	}
	if request.Name != (xml.Name{Space: "jabber:client", Local: "iq"}) || attrs["type"] != "get" || !xmppwire.BareJID(attrs["to"]) {
		return false
	}
	response := `<iq type="result" from="` + attrs["to"] + `" to="bridge@localhost/airc" id="` + attrs["id"] + `"><query xmlns="http://jabber.org/protocol/disco#info"><identity category="conference" type="text"/><feature var="http://jabber.org/protocol/muc"/><feature var="muc_nonanonymous"/><feature var="urn:xmpp:sid:0"/></query></iq>`
	switch mode {
	case "join_discovery_wrong_id":
		response = strings.Replace(response, attrs["id"], "uncorrelated", 1)
	case "join_discovery_wrong_room":
		response = strings.Replace(response, attrs["to"], "wrong@muc.localhost", 1)
	case "join_discovery_anonymous":
		response = strings.Replace(response, "muc_nonanonymous", "muc_semianonymous", 1)
	case "join_discovery_no_sid":
		response = strings.Replace(response, "urn:xmpp:sid:0", "urn:fake", 1)
	case "join_discovery_error":
		response = strings.Replace(response, `type="result"`, `type="error"`, 1)
	}
	_, err = io.WriteString(conn, response)
	return err == nil
}
