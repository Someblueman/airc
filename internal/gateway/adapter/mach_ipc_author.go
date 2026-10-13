package adapter

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/machipc"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// MachIPCAuthor admits unary plain author requests over Mach IPC (and the
// XPC and D-Bus bridges that forward to it). The kernel-audited caller UID
// is the access gate: only allowlisted UIDs, by default the gateway's own,
// are served. Each request names its author; the reply is one plain
// response frame on the message's one-shot reply right.
type MachIPCAuthor struct {
	d        *dispatch.Dispatcher
	requests chan struct{}
	uids     map[uint32]bool
}

// NewMachIPCAuthor builds the driver; an empty uids list admits only the
// gateway's own effective UID.
func NewMachIPCAuthor(d *dispatch.Dispatcher, requests chan struct{}, uids []uint32) (*MachIPCAuthor, error) {
	if d == nil || !d.AuthorAccess() || cap(requests) < 1 || len(uids) > 1024 {
		return nil, store.ErrInvalid
	}
	m := &MachIPCAuthor{d: d, requests: requests, uids: map[uint32]bool{}}
	if len(uids) == 0 {
		uids = []uint32{uint32(os.Geteuid())}
	}
	for _, uid := range uids {
		if uid == ^uint32(0) {
			return nil, store.ErrInvalid
		}
		m.uids[uid] = true
	}
	return m, nil
}

// Serve closes the ingress port and joins all admitted work before returning.
func (m *MachIPCAuthor) Serve(parent context.Context, listener *machipc.Listener) error {
	if m == nil || listener == nil {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	var work sync.WaitGroup
	defer func() {
		cancel()
		_ = listener.Close()
		work.Wait()
	}()
	for {
		message, err := listener.Receive(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, machipc.ErrInvalid) {
				continue
			}
			return err
		}
		// Check the kernel UID before parsing anything.
		if !m.uids[message.UID] {
			_ = message.Close()
			continue
		}
		request, plain, err := decodeFrame(message.Body)
		if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) || request.Profile != "" && request.Profile != "plain" || plain == nil {
			_ = message.Close()
			continue
		}
		write := m.writer(ctx, message)
		if request.Stream {
			_ = writeCorrelatedFailure(write, request.CorrelationID, store.ErrInvalid)
			_ = message.Close()
			continue
		}
		select {
		case m.requests <- struct{}{}:
			work.Go(func() {
				defer func() { <-m.requests }()
				defer message.Close()
				session := dispatch.NewSession()
				defer session.Close()
				runCorrelatedPlainRequest(ctx, write, nil, true, m.d, request, dispatch.Ingress{Session: session}, plain)
			})
		default:
			_ = writeCorrelatedFailure(func(response FrameResponse) error {
				response.Sequence = 1
				return write(response)
			}, request.CorrelationID, store.ErrCapacity)
			_ = message.Close()
		}
	}
}

func (m *MachIPCAuthor) writer(ctx context.Context, message *machipc.Request) func(FrameResponse) error {
	return func(response FrameResponse) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if response.Result != nil && response.Result.Capabilities != nil {
			response.Result.Capabilities.WatchModes = nil
		}
		body, err := encodeFrame(response)
		if err != nil || len(body) > machipc.MaxResponseBytes {
			return store.ErrCapacity
		}
		writeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
		defer finish()
		return message.Reply(writeCtx, body)
	}
}
