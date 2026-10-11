package auth

import (
	"context"
	"time"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

// VerifyRooms checks current membership only. It does not join rooms, cache
// authority, or prove membership at a later event's position in the timeline.
func (s *MatrixServer) VerifyRooms(parent context.Context) error {
	if parent == nil {
		return ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	if s.VerifyAccount(ctx) != nil {
		return ErrAuthentication
	}
	var response struct {
		Rooms []string `json:"joined_rooms"`
	}
	if s.getMatrixJSON(ctx, "/_matrix/client/v3/joined_rooms", &response, 65536) != nil || len(response.Rooms) > 256 {
		return ErrAuthentication
	}
	joined := make(map[string]bool, len(response.Rooms))
	for _, room := range response.Rooms {
		if !matrixwire.RoomID(room) || joined[room] {
			return ErrAuthentication
		}
		joined[room] = true
	}
	for room := range s.routes {
		if !joined[room] {
			return ErrAuthentication
		}
	}
	if ctx.Err() != nil {
		return ErrAuthentication
	}
	return nil
}
