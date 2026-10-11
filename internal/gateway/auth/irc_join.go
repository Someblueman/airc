package auth

import (
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/ircwire"
)

// JoinRooms joins only the immutable provisioned room set. Serial callers must
// finish this step before reading events; incomplete membership closes the session.
func (s *IRCSession) JoinRooms() (err error) {
	if s == nil || s.server == nil || s.ctx.Err() != nil || !s.registration.Ready() {
		return ErrAuthentication
	}
	defer func() {
		if err != nil {
			s.Close()
		}
	}()
	if s.conn.SetDeadline(time.Now().Add(3*time.Second)) != nil {
		return ErrAuthentication
	}
	rooms := make([]string, 0, len(s.server.routes))
	for room := range s.server.routes {
		rooms = append(rooms, room)
	}
	slices.Sort(rooms)
	lines := 0
	for _, room := range rooms {
		wire, e := ircwire.Encode("JOIN", []string{room}, nil)
		if e != nil || s.write([][]byte{wire}) != nil {
			return ErrAuthentication
		}
		for {
			lines++
			if lines > 512 {
				return ErrAuthentication
			}
			command, e := s.reader.Read()
			if e != nil || s.ctx.Err() != nil {
				return ErrAuthentication
			}
			wires, e := s.registration.Step(command)
			if e != nil || s.write(wires) != nil {
				return ErrAuthentication
			}
			if command.Name == "PRIVMSG" || ircJoinFailure(command.Name) {
				return ErrAuthentication
			}
			if command.Name != "JOIN" {
				continue
			}
			nick, rest, ok := strings.Cut(command.Prefix, "!")
			user, host, hostOK := strings.Cut(rest, "@")
			if !ok || !hostOK || user == "" || host == "" || strings.ContainsAny(rest, "! \t\r\n") || strings.Contains(host, "@") || !config.IRCNickname(nick) || !strings.EqualFold(nick, s.server.nickname) {
				continue
			}
			joined := ""
			if len(command.Params) == 1 && !command.HasTrailing {
				joined = command.Params[0]
			}
			if len(command.Params) == 0 && command.HasTrailing {
				joined = command.Trailing
			}
			if config.IRCRoom(joined) && strings.ToLower(joined) == room {
				break
			}
		}
	}
	return nil
}

// ircJoinFailure lists the numerics refusing a JOIN; the joining step sends no
// other command, so informational and unrelated numerics (such as 422) are ignored.
func ircJoinFailure(name string) bool {
	switch name {
	case "403", "405", "437", "461", // no such channel, too many channels, unavailable, missing parameter
		"471", "473", "474", "475", "476", "477", // full, invite-only, banned, key, bad mask, registered nick required
		"479", "489", "520": // common illegal-name, TLS-only and oper-only channel extensions
		return true
	}
	return false
}
