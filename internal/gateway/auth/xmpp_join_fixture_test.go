//go:build unix

package auth

import (
	"crypto/tls"
	"encoding/xml"
	"io"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

func xmppJoinScript(conn *tls.Conn, r *xmppwire.Stream, mode string) {
	if !strings.HasPrefix(mode, "join_") {
		return
	}
	for count := 0; count < 2; count++ {
		request, err := r.Read()
		if err != nil {
			return
		}
		to := ""
		for _, a := range request.Attributes {
			if a.Name == (xml.Name{Local: "to"}) {
				to = a.Value
			}
		}
		presence := `<presence from="` + to + `" to="bridge@localhost/airc"><x xmlns="http://jabber.org/protocol/muc#user"><item role="participant" affiliation="member"/><status code="100"/><status code="110"/></x></presence>`
		switch mode {
		case "join_anonymous":
			presence = strings.Replace(presence, `code="100"`, `code="170"`, 1)
		case "join_assigned":
			presence = strings.Replace(presence, `code="110"`, `code="210"`, 1)
		case "join_error":
			presence = strings.Replace(presence, `<presence `, `<presence type="error" `, 1)
		case "join_foreign":
			presence = strings.Replace(presence, `/airc" to=`, `/other" to=`, 1)
		case "join_early_message":
			presence = `<message from="` + to + `"><body>early</body></message>`
		case "join_budget":
			presence = strings.Repeat(strings.Replace(presence, `/airc" to=`, `/other" to=`, 1), 513)
		}
		if _, err = io.WriteString(conn, presence); err != nil {
			return
		}
		if mode != "join_valid" {
			return
		}
	}
}
