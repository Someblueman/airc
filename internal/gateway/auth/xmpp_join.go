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
	if !x.usable() {
		return ErrAuthentication
	}
	success := false
	defer func() {
		if !success {
			x.Close()
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	if x.conn.SetDeadline(deadline) != nil {
		return ErrAuthentication
	}
	x.joined = make(map[string]bool)
	x.occupants = make(map[string]string)
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
		if count >= 512 {
			return ErrAuthentication
		}
		count++
		if x.discoverRoom(room, deadline) != nil {
			return ErrAuthentication
		}
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
			if err != nil || !x.usable() || !time.Now().Before(deadline) {
				return ErrAuthentication
			}
			if e.Name != (xml.Name{Space: "jabber:client", Local: "presence"}) {
				return ErrAuthentication
			}
			if xmppwire.OwnJoined(e, room, x.server.account, x.server.resource) {
				x.joined[room] = true
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
			if !valid || bare != room || kind != "" && kind != "unavailable" || from == room+"/"+x.server.resource {
				return ErrAuthentication
			}
			if x.trackOccupant(e, room) != nil {
				return ErrAuthentication
			}
		}
	}
	if !x.usable() || !time.Now().Before(deadline) || x.conn.SetDeadline(time.Time{}) != nil {
		return ErrAuthentication
	}
	success = true
	return nil
}
