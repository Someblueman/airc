package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeIRC serially owns verified connections and admissions. The caller closes
// server after this owner joins. Disconnection or exhausted request capacity can
// lose events before intake; reconnect never claims to recover that loss.
func ServeIRC(ctx context.Context, server *auth.IRCServer, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if server == nil || d == nil || cap(slots) < 1 || cap(requests) < 1 {
		return store.ErrInvalid
	}
	return serveBridgeSessions(ctx, slots, func() bool { return serveIRCSession(ctx, server, d, requests) })
}

func serveIRCSession(ctx context.Context, server *auth.IRCServer, d *dispatch.Dispatcher, requests chan struct{}) bool {
	session, err := server.Connect(ctx)
	if err != nil {
		return false
	}
	defer session.Close()
	if session.JoinRooms() != nil {
		return false
	}
	progress := false
	for ctx.Err() == nil {
		proof, err := session.ReadEvent()
		if errors.Is(err, auth.ErrIRCEvent) {
			continue
		}
		if err != nil {
			return progress
		}
		select {
		case requests <- struct{}{}:
			completed, err := completeIRCEvent(ctx, session, d, proof)
			<-requests
			if err != nil {
				return progress
			}
			if completed {
				progress = true
			}
		default:
		}
	}
	return progress
}

func completeIRCEvent(ctx context.Context, session *auth.IRCSession, d *dispatch.Dispatcher, proof auth.VerifiedIRCEvent) (bool, error) {
	admission, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	e := proof.Event()
	result, err := d.Bridge(admission, e, time.Now())
	cancel() // Native read ownership has joined before publication.
	if e.IsRead() {
		return replyIRCRead(session, proof, result, err)
	}
	// Ordinary send refusal has always left the IRC session usable.
	return err == nil && result.Result != nil && result.Result.Durable, nil
}

// serveBridgeSessions runs one bridge session at a time while ctx lives, each
// holding a connection slot. Reconnects back off from 100ms, doubling to 5s,
// and reset after a session that made progress.
func serveBridgeSessions(ctx context.Context, slots chan struct{}, session func() (progress bool)) error {
	delay := 100 * time.Millisecond
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		progress := session()
		<-slots
		if progress {
			delay = 100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
	return nil
}
