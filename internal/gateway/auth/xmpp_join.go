package auth

import (
	"encoding/xml"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// JoinRooms serially requests only immutable provisioned rooms. One deadline
// and stanza budget cover all confirmations. Failure closes and joins the
// session; unconfirmed membership cannot be used as event authority.
func (x *XMPPSession) JoinRooms() error {
	if x == nil || x.conn == nil || x.server == nil || x.reader == nil {
		return ErrAuthentication
	}
	success := false
	defer func() {
		if !success {
			x.Close()
		}
	}()
	if x.conn.SetDeadline(time.Now().Add(3*time.Second)) != nil {
		return ErrAuthentication
	}
	rooms := map[string]bool{}
	for key := range x.server.grants {
		rooms[key[1]] = true
	}
	ordered := make([]string, 0, len(rooms))
	for room := range rooms {
		ordered = append(ordered, room)
	}
	slices.Sort(ordered)
	count := 0
	for _, room := range ordered {
		w, err := xmppwire.Join(room, x.server.resource)
		if err != nil || x.write(w) != nil {
			return ErrAuthentication
		}
		for {
			if count >= 512 {
				return ErrAuthentication
			}
			count++
			e, err := x.reader.Read()
			if err != nil {
				return ErrAuthentication
			}
			if e.Name != (xml.Name{Space: "jabber:client", Local: "presence"}) {
				return ErrAuthentication
			}
			if xmppwire.OwnJoined(e, room, x.server.account, x.server.resource) {
				break
			}
			from, kind := "", ""
			for _, a := range e.Attributes {
				if a.Name == (xml.Name{Local: "from"}) {
					from = a.Value
				}
				if a.Name == (xml.Name{Local: "type"}) {
					kind = a.Value
				}
			}
			bare, _, valid := xmppwire.FullJID(from)
			if !valid || bare != room || kind != "" || from == room+"/"+x.server.resource {
				return ErrAuthentication
			}
			// Other occupants' initial presence is not this own-join proof.
			// Durable author tracking remains a separate prerequisite.
		}
	}
	if x.conn.SetDeadline(time.Time{}) != nil {
		return ErrAuthentication
	}
	success = true
	return nil
}
