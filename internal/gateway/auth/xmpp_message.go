package auth

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// ErrXMPPMessage refuses a publication without destroying a usable session.
var ErrXMPPMessage = fmt.Errorf("XMPP message refused: %w", ErrAuthentication)

// VerifiedXMPPMessage is a groupchat message whose author comes from the real
// bare JID a non-anonymous room reported for the occupant on this session.
// Only the owning session can mint it; its zero value grants nothing.
type VerifiedXMPPMessage struct {
	server                                    *XMPPServer
	session                                   *XMPPSession
	event                                     BridgeEvent
	network, room, author, occupant, id, body string
}

func (m VerifiedXMPPMessage) Valid() bool        { return m.server != nil && m.event.Valid() }
func (m VerifiedXMPPMessage) Event() BridgeEvent { return m.event }
func (m VerifiedXMPPMessage) Room() string       { return m.room }

// RequestID includes the issuing room: XEP-0359 IDs are scoped to their issuer.
func (m VerifiedXMPPMessage) RequestID() string {
	if !m.Valid() {
		return ""
	}
	return m.event.RequestID()
}

// ReadMessage is serial session ownership. Idle waits end on parent cancellation;
// each started stanza has a three-second budget, and at most 128 control stanzas
// may precede an event. Whitespace starts the budget; buffered XML cannot renew it.
// Malformed presence, unknown rooms, I/O or membership loss close the session.
func (x *XMPPSession) ReadMessage() (VerifiedXMPPMessage, error) {
	if !x.usable() || len(x.joined) == 0 {
		return VerifiedXMPPMessage{}, ErrAuthentication
	}
	_, domain, _ := strings.Cut(x.server.account, "@")
	recipient := x.server.account + "/" + x.server.resource
	success := false
	defer func() {
		if !success {
			x.Close()
		}
	}()
	for range 128 {
		if x.conn.SetDeadline(time.Time{}) != nil {
			return VerifiedXMPPMessage{}, ErrAuthentication
		}
		var deadline time.Time
		e, err := x.reader.ReadStarted(func() error {
			deadline = time.Now().Add(3 * time.Second)
			return x.conn.SetDeadline(deadline)
		})
		if err != nil || !x.usable() || !time.Now().Before(deadline) {
			return VerifiedXMPPMessage{}, ErrAuthentication
		}
		if wire, ok := xmppwire.PingReply(e, domain, recipient); ok {
			if x.write(wire) != nil || !x.usable() || !time.Now().Before(deadline) {
				return VerifiedXMPPMessage{}, ErrAuthentication
			}
			continue
		}
		if room, ok := xmppwire.RoomSubject(e, recipient); ok && x.joined[room] {
			continue
		}
		from := ""
		for _, a := range e.Attributes {
			if a.Name == (xml.Name{Local: "from"}) {
				from = a.Value
			}
		}
		room, _, ok := xmppwire.FullJID(from)
		if !ok || !x.joined[room] {
			return VerifiedXMPPMessage{}, ErrAuthentication
		}
		if e.Name == (xml.Name{Space: "jabber:client", Local: "presence"}) {
			if from == room+"/"+x.server.resource {
				if !xmppwire.OwnPresent(e, room, x.server.account, x.server.resource) {
					return VerifiedXMPPMessage{}, ErrAuthentication
				}
			} else if x.trackOccupant(e, room) != nil {
				return VerifiedXMPPMessage{}, ErrAuthentication
			}
			continue
		}
		m, err := xmppwire.GroupMessage(e, room, x.server.account+"/"+x.server.resource)
		author, _, mapped := xmppwire.FullJID(x.occupants[from])
		route, routed := x.server.routes[room]
		var proof VerifiedXMPPMessage
		refusal := ErrXMPPMessage
		if err == nil && !m.Delayed && mapped && routed && author != x.server.account {
			// Live MUC traffic carries no server time; freshness starts on arrival.
			if bridged, ok := x.server.authors.Resolve(author); ok {
				requestID := ircDigest("airc-xmpp-message-v1", x.server.network, room, m.ID)
				event := newBridgeEvent(bridged, route, m.Body, requestID, time.Now().Add(x.server.freshness))
				if event.Valid() {
					proof = VerifiedXMPPMessage{server: x.server, session: x, event: event, network: x.server.network, room: room, author: author, occupant: from, id: m.ID, body: m.Body}
					refusal = nil
				}
			}
		}
		if !x.usable() || !time.Now().Before(deadline) || x.conn.SetDeadline(time.Time{}) != nil {
			return VerifiedXMPPMessage{}, ErrAuthentication
		}
		success = true
		return proof, refusal
	}
	return VerifiedXMPPMessage{}, ErrAuthentication
}
