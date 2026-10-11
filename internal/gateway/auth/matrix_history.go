package auth

import (
	"context"
	"github.com/Someblueman/airc/internal/gateway/matrixwire"
	"slices"
	"time"
)

// VerifiedMatrixHistory proves a bounded page from the configured authenticated
// homeserver. It is not event authority, complete recovery or durable progression.
type VerifiedMatrixHistory struct {
	server                                *MatrixServer
	room, from, to, direction, start, end string
	exhausted                             bool
	events                                []matrixwire.EventReference
}

func (p VerifiedMatrixHistory) Valid() bool                         { return p.server != nil && p.start != "" }
func (p VerifiedMatrixHistory) Room() string                        { return p.room }
func (p VerifiedMatrixHistory) From() string                        { return p.from }
func (p VerifiedMatrixHistory) To() string                          { return p.to }
func (p VerifiedMatrixHistory) Direction() string                   { return p.direction }
func (p VerifiedMatrixHistory) Start() string                       { return p.start }
func (p VerifiedMatrixHistory) End() string                         { return p.end }
func (p VerifiedMatrixHistory) Exhausted() bool                     { return p.Valid() && p.exhausted }
func (p VerifiedMatrixHistory) Events() []matrixwire.EventReference { return slices.Clone(p.events) }

// ReadHistory owns account/current room verification and one room-history GET
// under a single absolute deadline. Explicit from cursors must be echoed exactly.
func (s *MatrixServer) ReadHistory(parent context.Context, room, from, to, direction string) (VerifiedMatrixHistory, error) {
	path := matrixHistoryPath(room, from, to, direction)
	if s == nil || parent == nil || path == "" {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	if _, provisioned := s.routes[room]; !provisioned {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if s.VerifyRooms(ctx) != nil {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	data, err := s.getMatrixBody(ctx, path, 65536)
	defer clear(data)
	if err != nil {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	page, err := matrixwire.ParseHistory(data, room)
	if err != nil || from != "" && page.Start != from {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return VerifiedMatrixHistory{}, ErrAuthentication
	}
	return VerifiedMatrixHistory{server: s, room: room, from: from, to: to, direction: direction, start: page.Start, end: page.End, exhausted: page.Exhausted, events: slices.Clone(page.Events)}, nil
}
