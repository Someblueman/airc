package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// ServeXMPP owns serial verified sessions, signed admission and whole encrypted
// replies. Reconnection obtains a new roster; it cannot recover pre-intake loss.
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
			// durable admission. The sender may retry the same signed intent.
		}
	}
	return progress
}

// XMPPResult is protected in its entirety, including native request correlation
// and author/room routing. An outer stanza ID alone has no response authority.
type XMPPResult struct {
	Version   int                      `json:"version"`
	Protocol  string                   `json:"protocol"`
	RequestID string                   `json:"request_id"`
	Author    string                   `json:"author"`
	Room      string                   `json:"room"`
	Target    core.Target              `json:"target"`
	Result    *dispatch.DetachedResult `json:"result,omitempty"`
	Error     *core.Failure            `json:"error,omitempty"`
}

func completeXMPPMessage(ctx context.Context, session *auth.XMPPSession, d *dispatch.Dispatcher, proof auth.VerifiedXMPPMessage) (bool, error) {
	admission, cancel := context.WithTimeout(ctx, 3*time.Second)
	result, requestID, failure := d.XMPP(admission, proof, time.Now())
	cancel() // Native ownership joins before publication; request capacity is held.
	if requestID == "" {
		return false, nil
	}
	response := XMPPResult{Version: 1, Protocol: "xmpp", RequestID: requestID, Author: proof.Author(), Room: proof.Room(), Target: proof.Target()}
	if failure != nil {
		status, code := httpError(failure)
		response.Error = &core.Failure{Code: code, Transient: status == 429 || status == 503}
	} else {
		response.Result = &result
	}
	payload, err := json.Marshal(response)
	if err == nil {
		err = session.WriteResponse(proof, payload, time.Now())
	}
	completed := failure == nil
	if errors.Is(err, auth.ErrResponseCapacity) {
		// The whole protected stanza was refused before any write. Publish a
		// bounded error instead of truncating a page or inventing a cursor.
		completed = false
		response.Result = nil
		response.Error = &core.Failure{Code: core.CapacityExceeded, Transient: true}
		payload, err = json.Marshal(response)
		if err == nil {
			err = session.WriteResponse(proof, payload, time.Now())
		}
	}
	return completed && err == nil, err
}
