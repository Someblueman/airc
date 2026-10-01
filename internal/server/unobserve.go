package server

import (
	"github.com/Someblueman/airc/internal/protocol"
	"strings"
)

func (s *Server) unobserveLocked(client *session, command protocol.Command) {
	list, ok := command.Param(0)
	targets := strings.Split(list, ",")
	if !ok || len(targets) > 16 {
		s.numericLocked(client, "461", nil, "UNOBSERVE requires up to 16 targets")
		return
	}
	for _, target := range targets {
		key := target
		if strings.HasPrefix(target, "@") {
			key = "@" + nickKey(target[1:])
		}
		// Thread subscriptions are stored under the canonical root, which remains
		// removable even after the thread has left history.
		delete(client.watching, key)
		delete(s.watchers[key], client.client.ID)
		if len(s.watchers[key]) == 0 {
			delete(s.watchers, key)
		}
		s.numericLocked(client, "781", []string{target}, "No longer observing")
	}
}
