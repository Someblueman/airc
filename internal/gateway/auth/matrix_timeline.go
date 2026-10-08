package auth

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

var ErrMatrixTimelineLoss = fmt.Errorf("matrix timeline gap or departure: %w", ErrAuthentication)

// MatrixTimelineLoss retains diagnostic positions but no usable next cursor.
type MatrixTimelineLoss struct {
	gaps     []matrixwire.TimelineGap
	departed []string
}

func (e *MatrixTimelineLoss) Error() string                  { return ErrMatrixTimelineLoss.Error() }
func (e *MatrixTimelineLoss) Unwrap() error                  { return ErrMatrixTimelineLoss }
func (e *MatrixTimelineLoss) Gaps() []matrixwire.TimelineGap { return slices.Clone(e.gaps) }
func (e *MatrixTimelineLoss) Departed() []string             { return slices.Clone(e.departed) }

// VerifiedMatrixTimeline is immutable HTTP source provenance, not durable
// progression or event authority. Individual references require ReadMessage.
type VerifiedMatrixTimeline struct {
	server      *MatrixServer
	since, next string
	events      []matrixwire.EventReference
}

func (p VerifiedMatrixTimeline) Valid() bool { return p.server != nil && p.next != "" }
func (p VerifiedMatrixTimeline) Cursor() string {
	if !p.Valid() {
		return ""
	}
	return p.next
}
func (p VerifiedMatrixTimeline) Since() string                       { return p.since }
func (p VerifiedMatrixTimeline) Initial() bool                       { return p.Valid() && p.since == "" }
func (p VerifiedMatrixTimeline) Events() []matrixwire.EventReference { return slices.Clone(p.events) }

// ReadTimeline owns account, current membership and one filtered sync request
// under a shared deadline. Gaps/departures never yield a usable page/cursor.
func (s *MatrixServer) ReadTimeline(parent context.Context, since string) (VerifiedMatrixTimeline, error) {
	if s == nil || parent == nil || since != "" && !matrixwire.Cursor(since) {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	rooms := map[string]bool{}
	for pair := range s.grants {
		rooms[pair[1]] = true
	}
	ordered := make([]string, 0, len(rooms))
	for room := range rooms {
		ordered = append(ordered, room)
	}
	path := matrixSyncPath(since, ordered)
	if path == "" {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	if s.VerifyRooms(ctx) != nil {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	data, err := s.getMatrixBody(ctx, path, 65536)
	defer clear(data)
	if err != nil {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	page, err := matrixwire.ParseTimeline(data)
	if err != nil {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	for _, ref := range page.Events {
		if !rooms[ref.Room] {
			return VerifiedMatrixTimeline{}, ErrAuthentication
		}
	}
	for _, gap := range page.Gaps {
		if !rooms[gap.Room] {
			return VerifiedMatrixTimeline{}, ErrAuthentication
		}
	}
	for _, room := range page.Departed {
		if !rooms[room] {
			return VerifiedMatrixTimeline{}, ErrAuthentication
		}
	}
	if ctx.Err() != nil || !time.Now().Before(deadline) {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	if len(page.Gaps) > 0 || len(page.Departed) > 0 {
		return VerifiedMatrixTimeline{}, &MatrixTimelineLoss{gaps: slices.Clone(page.Gaps), departed: slices.Clone(page.Departed)}
	}
	if since == page.Next && len(page.Events) > 0 {
		return VerifiedMatrixTimeline{}, ErrAuthentication
	}
	return VerifiedMatrixTimeline{server: s, since: since, next: page.Next, events: slices.Clone(page.Events)}, nil
}
