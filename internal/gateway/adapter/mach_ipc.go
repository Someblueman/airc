package adapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/machipc"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// MachCaller grants a kernel-audited effective UID permission to present one
// configured signing principal. Neither the UID nor a port grants AIRC identity.
type MachCaller struct {
	UID       uint32
	Principal string
}

// MachIPC admits unary encrypted requests and owns their one-shot reply rights.
type MachIPC struct {
	d          *dispatch.Dispatcher
	requests   chan struct{}
	protection *ConnectionResponseProtection
	callers    map[uint32]map[string]bool
}

func NewMachIPC(d *dispatch.Dispatcher, requests chan struct{}, protection *ConnectionResponseProtection, callers []MachCaller) (*MachIPC, error) {
	if d == nil || cap(requests) < 1 || protection == nil || protection.sealer == nil || len(protection.routes) == 0 || len(callers) < 1 || len(callers) > 1024 {
		return nil, store.ErrInvalid
	}
	m := &MachIPC{d: d, requests: requests, protection: protection, callers: make(map[uint32]map[string]bool)}
	for _, caller := range callers {
		if caller.UID == ^uint32(0) || !validUDPID(caller.Principal) || m.callers[caller.UID][caller.Principal] {
			return nil, store.ErrInvalid
		}
		if m.callers[caller.UID] == nil {
			m.callers[caller.UID] = make(map[string]bool)
		}
		m.callers[caller.UID][caller.Principal] = true
	}
	return m, nil
}

// Serve closes the ingress port and joins all admitted work before returning.
// Authentication is serial and work is admitted before starting a goroutine.
func (m *MachIPC) Serve(parent context.Context, listener *machipc.Listener) error {
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
		request, verified, err := m.authenticate(message.Body, message.UID)
		if err != nil {
			_ = message.Close()
			continue
		}
		select {
		case m.requests <- struct{}{}:
			work.Go(func() {
				defer func() { <-m.requests }()
				defer message.Close()
				requestCtx, finish := context.WithTimeout(ctx, 4*time.Second)
				defer finish()
				runCorrelatedVerifiedRequest(requestCtx, m.writer(requestCtx, message, verified), nil, true, m.d, request, verified)
			})
		default:
			_ = writeCorrelatedFailure(func(response FrameResponse) error {
				response.Sequence = 1
				return m.writer(ctx, message, verified)(response)
			}, request.CorrelationID, store.ErrCapacity)
			_ = message.Close()
		}
	}
}

func (m *MachIPC) authenticate(body []byte, uid uint32) (FrameRequest, dispatch.VerifiedMessage, error) {
	var request FrameRequest
	var verified dispatch.VerifiedMessage
	// Check the kernel UID before spending work on envelope cryptography.
	if len(m.callers[uid]) == 0 || jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || request.Stream || request.Profile != "encrypted" || request.Envelope == "" {
		return request, verified, store.ErrInvalid
	}
	digest := sha256.Sum256([]byte(request.Envelope))
	if request.CorrelationID != hex.EncodeToString(digest[:]) {
		return request, verified, store.ErrInvalid
	}
	verified, err := m.d.AuthenticateDetached(request.Envelope, true, time.Now())
	if err != nil {
		return request, verified, err
	}
	if !m.callers[uid][verified.PrincipalID()] {
		return request, verified, store.ErrInvalid
	}
	if _, ok := m.protection.routes[verified.PrincipalID()+"\x00"+verified.Binding()]; !ok {
		return request, verified, store.ErrInvalid
	}
	return request, verified, nil
}

func (m *MachIPC) writer(ctx context.Context, message *machipc.Request, verified dispatch.VerifiedMessage) func(FrameResponse) error {
	return func(response FrameResponse) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if response.Result != nil && response.Result.Capabilities != nil {
			response.Result.Capabilities.WatchModes = nil
		}
		token, err := m.protection.seal(verified, response)
		if errors.Is(err, auth.ErrResponseCapacity) {
			return store.ErrCapacity
		}
		if err != nil {
			return err
		}
		writeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return message.Reply(writeCtx, []byte(token))
	}
}
