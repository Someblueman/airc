package adapter

import (
	"context"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeXMPP owns serial verified sessions, plain-message admission and whole
// plain read replies. Reconnection obtains a new roster; it cannot recover pre-intake loss.
// The caller joins this owner before clearing the server's credentials.
func ServeXMPP(ctx context.Context, server *auth.XMPPServer, d *dispatch.Dispatcher, slots, requests chan struct{}) error {
	if server == nil || d == nil || cap(slots) < 1 || cap(requests) < 1 {
		return store.ErrInvalid
	}
	delay := 100 * time.Millisecond
	for ctx.Err() == nil {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			return nil
		}
		progress := serveXMPPSession(ctx, server, d, requests)
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

func serveXMPPSession(ctx context.Context, server *auth.XMPPServer, d *dispatch.Dispatcher, requests chan struct{}) bool {
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
		proof, err := session.ReadMessage()
		if errors.Is(err, auth.ErrXMPPMessage) {
			continue
		}
		if err != nil {
			return progress
		}
		select {
		case requests <- struct{}{}:
			completed, err := completeXMPPMessage(ctx, session, d, proof)
			<-requests
			if err != nil {
				return progress
			}
			progress = progress || completed
		default:
			// No provider acknowledgement or cursor promises delivery before
			// durable admission; a message arriving without capacity is lost.
		}
	}
	return progress
}

func completeXMPPMessage(ctx context.Context, session *auth.XMPPSession, d *dispatch.Dispatcher, proof auth.VerifiedXMPPMessage) (bool, error) {
	admission, cancel := context.WithTimeout(ctx, 3*time.Second)
	e := proof.Event()
	result, failure := d.Bridge(admission, e, time.Now())
	cancel() // Native ownership joins before publication; request capacity is held.
	if !e.IsRead() {
		// A plain message is a send; the room itself is the sender's echo.
		return failure == nil && result.Result != nil && result.Result.Durable, nil
	}
	response := bridgeResult("xmpp", proof.Room(), e, result, failure)
	err := writeBridgeResult(response, func(payload []byte) error {
		return session.WriteResponse(proof, payload, time.Now())
	})
	return failure == nil && err == nil, err
}
