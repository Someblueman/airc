package auth

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
	"github.com/Someblueman/airc/internal/protocol"
)

// ErrIRCEvent marks a refused publication on an otherwise usable session.
// It retains the authentication refusal classification.
var ErrIRCEvent = fmt.Errorf("IRC event refused: %w", ErrAuthentication)

// VerifiedIRCEvent is a channel PRIVMSG from a verified server session whose
// author comes from the server's account tag. Its zero value grants nothing;
// callers cannot supply parsed lines to mint it.
type VerifiedIRCEvent struct {
	server                                    *IRCServer
	event                                     BridgeEvent
	network, eventID, account, room, nickname string
	body                                      string
	occurred                                  time.Time
}

func (e VerifiedIRCEvent) Valid() bool               { return e.server != nil && e.event.Valid() }
func (e VerifiedIRCEvent) Event() BridgeEvent        { return e.event }
func (e VerifiedIRCEvent) Body() string              { return e.body }
func (e VerifiedIRCEvent) Account() string           { return e.account }
func (e VerifiedIRCEvent) Room() string              { return e.room }
func (e VerifiedIRCEvent) Nickname() string          { return e.nickname }
func (e VerifiedIRCEvent) Occurred() time.Time       { return e.occurred }
func (e VerifiedIRCEvent) ReplayDeadline() time.Time { return e.event.ReplayDeadline() }
func (e VerifiedIRCEvent) RequestID() string {
	if !e.Valid() {
		return ""
	}
	return e.event.RequestID()
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

// ReadEvent owns the source read and control processing. Each call handles at
// most 128 lines or keepalive expiries; reaching that bound returns ErrIRCEvent
// with the session still usable. A quiet link stays open: after the keepalive
// interval without traffic it sends one PING, and only a further quiet timeout
// ends the session. Each write has a three-second budget. Authentication
// refusals leave the session usable; I/O/control errors close it.
func (s *IRCSession) ReadEvent() (VerifiedIRCEvent, error) {
	if s == nil || s.server == nil || !s.registration.Ready() || s.ctx.Err() != nil {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	probing := false
	for range 128 {
		quiet := s.server.keepalive
		if probing {
			quiet = s.server.keepaliveTimeout
		}
		if err := s.conn.SetReadDeadline(time.Now().Add(quiet)); err != nil {
			s.Close()
			return VerifiedIRCEvent{}, err
		}
		c, err := s.reader.Read()
		if errors.Is(err, ircwire.ErrIdle) && !probing && s.ctx.Err() == nil {
			probing = true
			if err = s.writeWithin(ircKeepalive()); err != nil {
				s.Close()
				return VerifiedIRCEvent{}, err
			}
			continue
		}
		if err != nil {
			s.Close()
			return VerifiedIRCEvent{}, err
		}
		probing = false
		wires, err := s.registration.Step(c)
		if err == nil {
			err = s.writeWithin(wires)
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
	return VerifiedIRCEvent{}, ErrIRCEvent
}

func ircKeepalive() [][]byte {
	wire, _ := ircwire.Encode("PING", []string{"airc-keepalive"}, nil)
	return [][]byte{wire}
}

// verifyEvent accepts only a server-vouched account: an IRC nickname alone
// is not an identity. The author is the mapped AIRC account or the guest
// nickname derived from that account, never the IRC nickname.
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
	route, ok := s.routes[room]
	if !ok {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	author, ok := s.authors.Resolve(account)
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
	requestID := ircDigest("airc-irc-event-v1", s.network, id)
	event := newBridgeEvent(author, route, c.Trailing, requestID, occurred.Add(s.freshness))
	if !event.Valid() {
		return VerifiedIRCEvent{}, ErrAuthentication
	}
	return VerifiedIRCEvent{server: s, event: event, network: s.network, eventID: id, account: account, room: room, nickname: nick, body: c.Trailing, occurred: occurred}, nil
}
