package auth

import (
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/xmppwire"
)

// ErrXMPPMessage refuses a publication without destroying a usable session.
var ErrXMPPMessage = fmt.Errorf("XMPP message refused: %w", ErrAuthentication)

// VerifiedXMPPMessage proves current server-session author/room provenance.
// It does not prove original-event freshness or journal admission. Only the
// owning session can mint it; its zero value grants nothing.
type VerifiedXMPPMessage struct {
	server                                    *XMPPServer
	session                                   *XMPPSession
	identity                                  Identity
	target                                    core.Target
	operations                                []core.Operation
	network, room, author, occupant, id, body string
}

func (m VerifiedXMPPMessage) Valid() bool                  { return m.server != nil && m.identity.ID() != "" }
func (m VerifiedXMPPMessage) Identity() Identity           { return m.identity }
func (m VerifiedXMPPMessage) Target() core.Target          { return m.target }
func (m VerifiedXMPPMessage) Operations() []core.Operation { return slices.Clone(m.operations) }
func (m VerifiedXMPPMessage) Room() string                 { return m.room }
func (m VerifiedXMPPMessage) Author() string               { return m.author }
func (m VerifiedXMPPMessage) Occupant() string             { return m.occupant }
func (m VerifiedXMPPMessage) Body() string                 { return m.body }

// RequestID includes the issuing room: XEP-0359 IDs are scoped to their issuer.
func (m VerifiedXMPPMessage) RequestID() string {
	if !m.Valid() {
		return ""
	}
	return ircDigest("airc-xmpp-message-v1", m.network, m.room, m.id)
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
		grant, authorized := x.server.grants[[2]string{author, room}]
		var proof VerifiedXMPPMessage
		refusal := ErrXMPPMessage
		if err == nil && mapped && authorized && author != x.server.account {
			proof = VerifiedXMPPMessage{server: x.server, session: x, identity: grant.identity, target: grant.target, operations: slices.Clone(grant.operations), network: x.server.network, room: room, author: author, occupant: from, id: m.ID, body: m.Body}
			refusal = nil
		}
		if !x.usable() || !time.Now().Before(deadline) || x.conn.SetDeadline(time.Time{}) != nil {
			return VerifiedXMPPMessage{}, ErrAuthentication
		}
		success = true
		return proof, refusal
	}
	return VerifiedXMPPMessage{}, ErrAuthentication
}
