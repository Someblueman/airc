package auth

import (
	"strings"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// joinControl consumes bounded startup traffic from rooms already confirmed on
// this session. Publications are intentionally lost before the session becomes
// ready; no admission or archive lookup occurs here.
func (x *XMPPSession) joinControl(e *xmppwire.Element) (bool, error) {
	if x == nil || x.server == nil || e == nil {
		return false, ErrAuthentication
	}
	recipient := x.server.account + "/" + x.server.resource
	_, domain, _ := strings.Cut(x.server.account, "@")
	if wire, ok := xmppwire.PingReply(e, domain, recipient); ok {
		if x.write(wire) != nil || !x.usable() {
			return false, ErrAuthentication
		}
		return true, nil
	}
	if room, ok := xmppwire.RoomSubject(e, recipient); ok {
		if x.joined[room] {
			return true, nil
		}
		return false, nil
	}
	if e.Name.Space != "jabber:client" {
		return false, nil
	}
	from := ""
	for _, attr := range e.Attributes {
		if attr.Name.Space == "" && attr.Name.Local == "from" {
			from = attr.Value
		}
	}
	room, _, valid := xmppwire.FullJID(from)
	if !valid || !x.joined[room] {
		return false, nil
	}
	switch e.Name.Local {
	case "presence":
		if from == room+"/"+x.server.resource {
			if !xmppwire.OwnPresent(e, room, x.server.account, x.server.resource) {
				return false, ErrAuthentication
			}
			return true, nil
		}
		if err := x.trackOccupant(e, room); err != nil {
			return false, err
		}
		return true, nil
	case "message":
		if _, err := xmppwire.GroupMessage(e, room, recipient); err != nil {
			return false, err
		}
		return true, nil
	case "iq":
		// Startup IQs from a room are not part of this session's controls.
		return false, nil
	default:
		return false, nil
	}
}
