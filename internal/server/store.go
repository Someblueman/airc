package server

import (
	"log/slog"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// A store is one feature's state, embedded in Server so its fields and
// methods keep their names. Every field of every store is read and written
// with s.mu held, and its saves run under s.mu too, as they always have;
// no store holds a *Server, so anything that needs live sessions, another
// store or a numeric reply stays a Server method.

// storeEnv is what a store needs from the server: the clock (live, so tests
// that set cfg.Now keep working), the two durability policies and the log.
type storeEnv struct {
	now    func() time.Time
	sync   atomicfile.Sync // credentials, chat, moderation, handoffs, decisions: as configured
	soft   atomicfile.Sync // headers, profiles, wakes: never a device flush (see softSync)
	logger *slog.Logger
}

// persisted is a feature's file-backed state: loaded once before Serve and
// closed once from Shutdown, both with s.mu held. A store with no file
// (path "") keeps its state in memory and saves are no-ops, as today.
type persisted interface {
	load(path string) error
	close() error
}

// storesLocked lists the stores Shutdown closes, in the order it closed
// them before they were stores.
func (s *Server) storesLocked() []persisted {
	return []persisted{&s.accountsState, &s.moderationState, &s.directoryState, &s.topicState, &s.chatStore, &s.handoffState, &s.decisionState}
}
