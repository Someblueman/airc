package auth

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/protocol"
)

// ErrIRCEvent marks a refused publication on an otherwise usable session.
// It retains the authentication refusal classification.
var ErrIRCEvent = fmt.Errorf("IRC event refused: %w", ErrAuthentication)

type ircGrant struct {
	identity                   Identity
	target                     core.Target
	operations                 []core.Operation
	responseRecipientKeyID     string
	responseRecipientPublicKey *ecdsa.PublicKey
}

// VerifiedIRCEvent is immutable server-session provenance, not journal intake.
// Its zero value grants nothing; callers cannot supply parsed lines to mint it.
type VerifiedIRCEvent struct {
	server                                          *IRCServer
	identity                                        Identity
	target                                          core.Target
	operations                                      []core.Operation
	readRequest                                     *core.Request
	readCorrelation                                 string
	network, eventID, account, room, nickname, body string
	occurred, until                                 time.Time
}

func (e VerifiedIRCEvent) Valid() bool                  { return e.server != nil && e.identity.ID() != "" }
func (e VerifiedIRCEvent) Identity() Identity           { return e.identity }
func (e VerifiedIRCEvent) Target() core.Target          { return e.target }
func (e VerifiedIRCEvent) Operations() []core.Operation { return slices.Clone(e.operations) }
func (e VerifiedIRCEvent) Body() string                 { return e.body }
func (e VerifiedIRCEvent) Account() string              { return e.account }
func (e VerifiedIRCEvent) Room() string                 { return e.room }
func (e VerifiedIRCEvent) Nickname() string             { return e.nickname }
func (e VerifiedIRCEvent) Occurred() time.Time          { return e.occurred }
func (e VerifiedIRCEvent) ReplayDeadline() time.Time    { return e.until }
func (e VerifiedIRCEvent) IsRead() bool                 { return e.Valid() && e.readRequest != nil }
func (e VerifiedIRCEvent) ReadCorrelationID() string {
	if !e.IsRead() {
		return ""
	}
	return e.readCorrelation
}
func (e VerifiedIRCEvent) ReadRequest() (core.Request, error) {
	if !e.IsRead() || e.server.verifier == nil || !e.server.verifier.OwnsIdentity(e.identity) {
		return core.Request{}, ErrIRCEvent
	}
	request := *e.readRequest
	if request.Target != nil {
		target := *request.Target
		request.Target = &target
	}
	if !isIRCReadOperation(request.Operation) {
		return core.Request{}, ErrIRCEvent
	}
	if err := e.identity.Authorize(request); err != nil || request.Target == nil || *request.Target != e.target {
		return core.Request{}, ErrIRCEvent
	}
	return request, nil
}
func (e VerifiedIRCEvent) RequestID() string {
	if !e.Valid() {
		return ""
	}
	return ircDigest("airc-irc-event-v1", e.network, e.eventID)
}

// Fingerprint excludes mutable IRC nick/host metadata. Event ID, account, room,
// exact server timestamp, body and mapped native authority must remain unchanged.
func (e VerifiedIRCEvent) Fingerprint() string {
	if !e.Valid() {
		return ""
	}
	parts := []string{"airc-irc-content-v1", e.network, e.eventID, e.account, e.room, e.occurred.Format(time.RFC3339Nano), e.body, e.identity.ID(), e.identity.Binding(), e.target.Kind, e.target.Name}
	if e.readRequest != nil {
		fingerprint, err := e.readRequest.Fingerprint()
		if err != nil {
			return ""
		}
		parts = append(parts, fingerprint, e.readCorrelation)
	}
	return ircDigest(parts...)
}
func ircDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReadEvent owns the source read and control processing. Each call has one
// three-second read/write budget and a 128-line bound; PING cannot extend it.
// Authentication refusals leave the session usable; I/O/control errors close it.
func (s *IRCSession) ReadEvent() (VerifiedIRCEvent, error) {
	if s == nil || s.server == nil || !s.registration.Ready() || s.ctx.Err() != nil {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	if err := s.conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		s.Close()
		return VerifiedIRCEvent{}, err
	}
	for range 128 {
		c, err := s.reader.Read()
		if err != nil {
			s.Close()
			return VerifiedIRCEvent{}, err
		}
		wires, err := s.registration.Step(c)
		if err == nil {
			err = s.write(wires)
		}
		if err != nil {
			s.Close()
			return VerifiedIRCEvent{}, err
		}
		if s.ctx.Err() != nil {
			s.Close()
			return VerifiedIRCEvent{}, ErrAuthentication
		}
		if c.Name == "PRIVMSG" {
			event, err := s.server.verifyEvent(c, time.Now())
			if err != nil {
				return VerifiedIRCEvent{}, ErrIRCEvent
			}
			return event, nil
		}
	}
	s.Close()
	return VerifiedIRCEvent{}, ErrAuthentication
}

func (s *IRCServer) verifyEvent(c protocol.Command, now time.Time) (VerifiedIRCEvent, error) {
	if s == nil || c.Name != "PRIVMSG" || len(c.Params) != 1 || !c.HasTrailing || !utf8.ValidString(c.Trailing) || strings.TrimSpace(c.Trailing) == "" || strings.ContainsAny(c.Trailing, "\x00\r\n\x01") {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	nick, rest, ok := strings.Cut(c.Prefix, "!")
	user, host, hostOK := strings.Cut(rest, "@")
	if !ok || !hostOK || !config.IRCNickname(nick) || user == "" || host == "" || strings.ContainsAny(rest, "! \t\r\n") || strings.Contains(host, "@") {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	account := c.Tags["account"]
	if !config.IRCAccount(account) || strings.EqualFold(account, s.account) || !config.IRCRoom(c.Params[0]) {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	room := strings.ToLower(c.Params[0])
	grant, ok := s.grants[[2]string{account, room}]
	if !ok {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	id := c.Tags["msgid"]
	if len(id) == 0 || len(id) > 256 || id[0] == ':' || strings.ContainsAny(id, " \r\n\x00") {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	stamp := c.Tags["time"]
	occurred, err := time.Parse("2006-01-02T15:04:05.000Z", stamp)
	if err != nil || occurred.Format("2006-01-02T15:04:05.000Z") != stamp || occurred.Unix() < 0 || now.Before(occurred) || !now.Before(occurred.Add(s.freshness)) {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	readTag, hasReadTag := c.Tags["+airc/read"]
	readMarker := strings.HasPrefix(c.Trailing, ircwire.ReadMarker)
	event := VerifiedIRCEvent{server: s, identity: grant.identity, target: grant.target, operations: slices.Clone(grant.operations), network: s.network, eventID: id, account: account, room: room, nickname: nick, body: c.Trailing, occurred: occurred, until: occurred.Add(s.freshness)}
	if !readMarker && !hasReadTag {
		return event, nil
	}
	if !readMarker || !hasReadTag || c.Trailing != ircwire.ReadMarker || grant.responseRecipientPublicKey == nil || grant.responseRecipientKeyID == "" || s.responseSealer == nil {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	read, err := ircwire.DecodeReadRequest(readTag)
	if err != nil || read.Operation != core.ContextRead && read.Operation != core.HistoryRead && read.Operation != core.Search || !slices.Contains(grant.operations, read.Operation) {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	target := grant.target
	request := core.Request{
		Version: 1, RequestID: event.RequestID(), Operation: read.Operation, Target: &target,
		Cursor: read.Cursor, Limit: read.Limit, Query: read.Query, From: read.From,
		ParentID: read.ParentID, ThreadMode: read.ThreadMode,
	}
	if err := request.Normalize(); err != nil || grant.identity.Authorize(request) != nil {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	event.readRequest = &request
	event.readCorrelation = read.CorrelationID
	return event, nil
}
