package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
)

type coapWatch struct {
	cancel    context.CancelFunc
	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	mid       int32
}

func serveCoAPObserve(ctx context.Context, w coapResponder, peer coapPeer, req *pool.Message, d *dispatch.Dispatcher, state *coapState) {
	if req.Code() != codes.GET {
		_ = w.SetResponse(codes.MethodNotAllowed, message.TextPlain, nil)
		return
	}
	bodySize, err := req.BodySize()
	observe, observeErr := req.Observe()
	if err != nil || bodySize != 0 || observeErr != nil || observe > 1 || len(req.Token()) != 8 {
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	token := append(message.Token(nil), req.Token()...)
	key := string(token)
	if observe == 1 {
		state.mu.Lock()
		watch := state.active[key]
		state.mu.Unlock()
		if watch == nil {
			_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
			return
		}
		watch.cancel()
		select {
		case <-watch.done:
		case <-ctx.Done():
			return
		}
		_ = coapJSON(w, codes.Content, map[string]any{"version": 1, "event": "end", "reason": "cancelled"})
		return
	}
	if !state.admit() {
		_ = w.SetResponse(codes.ServiceUnavailable, message.TextPlain, nil)
		return
	}
	transferred := false
	defer func() {
		if !transferred {
			state.release()
		}
	}()
	watchCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	watch := &coapWatch{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{}), mid: -1}
	state.mu.Lock()
	pending, ok := state.pending[key]
	if !ok || state.closed || !time.Now().Before(pending.expires) {
		delete(state.pending, key)
		if ok {
			delete(state.correlations, pending.request.CorrelationID)
		}
		state.mu.Unlock()
		cancel()
		_ = w.SetResponse(codes.BadRequest, message.TextPlain, nil)
		return
	}
	delete(state.pending, key)
	state.active[key] = watch
	state.authenticated[key] = true
	state.workers.Add(1)
	state.mu.Unlock()
	transferred = true
	cc := peer
	go func() {
		defer state.workers.Done()
		defer state.release()
		defer cancel()
		defer close(watch.done)
		defer func() {
			state.mu.Lock()
			delete(state.active, key)
			delete(state.correlations, pending.request.CorrelationID)
			state.mu.Unlock()
		}()
		// The actual carrier write of Observe=0 releases this gate, not scheduling or
		// a timer. Thus the first logical page cannot precede the ready marker.
		select {
		case <-watch.ready:
		case <-watchCtx.Done():
			return
		}
		var sequence uint32 = 1
		write := func(response FrameResponse) error {
			body, err := json.Marshal(response)
			if err != nil || len(body) > coapMaxResponse {
				return store.ErrCapacity
			}
			fragments, err := fragmentAGD1(body)
			if err != nil {
				return err
			}
			writeCtx, finish := context.WithTimeout(watchCtx, 3*time.Second)
			defer finish()
			for _, fragment := range fragments {
				if err := writeCtx.Err(); err != nil {
					return err
				}
				msg := cc.AcquireMessage(writeCtx)
				msg.SetType(message.Confirmable)
				msg.SetCode(codes.Content)
				msg.SetToken(token)
				msg.SetContentFormat(message.AppOctets)
				msg.SetOptionUint32(message.MaxAge, 0)
				msg.SetObserve(sequence)
				msg.SetBody(bytes.NewReader(fragment))
				err := cc.WriteMessage(msg)
				cc.ReleaseMessage(msg)
				if err != nil {
					// A cancelled subscription needs no carrier failure. A lost ACK
					// or failed write closes the peer so partial output cannot look
					// like an indefinitely healthy observation.
					if watchCtx.Err() == nil {
						_ = cc.Close()
						cancel()
					}
					return err
				}
				sequence = (sequence + 1) & 0xffffff
			}
			return writeCtx.Err()
		}
		runCorrelatedVerifiedRequest(watchCtx, write, []string{"long_poll", state.watchMode}, true, d, pending.request, pending.verified)
	}()
	if err := coapJSON(w, codes.Content, map[string]any{"version": 1, "correlation_id": pending.request.CorrelationID, "event": "ready"}); err != nil {
		cancel()
		return
	}
	w.Message().SetObserve(0)
}
