package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// correlatedTransport is the small protocol boundary shared by framed and
// WebSocket connections. The transport owns framing, deadlines, and closing;
// this loop owns validation, admission, dispatch, and stream budgets.
type correlatedTransport struct {
	next        func() ([]byte, error)
	write       func(FrameResponse) error
	watchModes  []string
	gracefulEOF bool
	sequenced   bool
}

func runCorrelated(ctx context.Context, transport correlatedTransport, d *dispatch.Dispatcher, requests chan struct{}, encryptedOnly bool) error {
	if cap(requests) < 1 || transport.next == nil || transport.write == nil {
		return errors.New("invalid correlated transport or request capacity")
	}
	ctx, cancel := context.WithCancel(ctx)
	var work sync.WaitGroup
	drain := false
	defer func() {
		if !drain {
			cancel()
		}
		work.Wait()
		cancel()
	}()
	active := make(map[string]bool)
	var activeMu sync.Mutex
	fail := func(id string, err error) error {
		status, code := httpError(err)
		sequence := uint64(0)
		if transport.sequenced {
			sequence = 1
		}
		return transport.write(FrameResponse{Version: 1, CorrelationID: id, Sequence: sequence, Event: "error", Error: &core.Failure{Code: code, Transient: status == 429 || status == 503}})
	}
	for {
		body, err := transport.next()
		if err != nil {
			if transport.gracefulEOF && errors.Is(err, errFramedCleanEOF) {
				drain = true
				return nil
			}
			return err
		}
		var request FrameRequest
		if jsoninput.Decode(body, &request, MaxRequestFrameBytes) != nil || request.Version != 1 || !correlationID(request.CorrelationID) {
			return store.ErrInvalid
		}
		// Correlation collisions are checked before profile and envelope
		// validation, so a rejected request can never alias an active stream.
		activeMu.Lock()
		duplicate := active[request.CorrelationID]
		activeMu.Unlock()
		if duplicate {
			return store.ErrInvalid
		}
		if request.Envelope == "" || (request.Profile != "signed" && request.Profile != "encrypted") {
			if err := fail(request.CorrelationID, store.ErrInvalid); err != nil {
				return err
			}
			continue
		}
		if encryptedOnly && request.Profile != "encrypted" {
			if err := fail(request.CorrelationID, store.ErrInvalid); err != nil {
				return err
			}
			continue
		}
		activeMu.Lock()
		full := len(active) >= 4
		if !full {
			active[request.CorrelationID] = true
		}
		activeMu.Unlock()
		if full {
			if err := fail(request.CorrelationID, store.ErrCapacity); err != nil {
				return err
			}
			continue
		}
		select {
		case requests <- struct{}{}:
		default:
			activeMu.Lock()
			delete(active, request.CorrelationID)
			activeMu.Unlock()
			if err := fail(request.CorrelationID, store.ErrCapacity); err != nil {
				return err
			}
			continue
		}
		work.Go(func() {
			defer func() {
				<-requests
				activeMu.Lock()
				delete(active, request.CorrelationID)
				activeMu.Unlock()
			}()
			runCorrelatedRequest(ctx, transport.write, transport.watchModes, transport.sequenced, d, request)
		})
	}
}

func runCorrelatedRequest(parent context.Context, write func(FrameResponse) error, watchModes []string, sequenced bool, d *dispatch.Dispatcher, request FrameRequest) {
	timeout := 4 * time.Second
	if request.Stream {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	verified, err := d.AuthenticateDetached(request.Envelope, request.Profile == "encrypted", time.Now())
	if d == nil {
		err = store.ErrInvalid
	}
	if err != nil {
		if sequenced {
			original := write
			write = func(response FrameResponse) error { response.Sequence = 1; return original(response) }
		}
		_ = writeCorrelatedFailure(write, request.CorrelationID, err)
		return
	}
	runCorrelatedVerifiedRequest(ctx, write, watchModes, sequenced, d, request, verified)
}

// Packet transports select an owner response route after authentication and
// pass the opaque intent here without parsing or verifying it a second time.
func runCorrelatedVerifiedRequest(parent context.Context, write func(FrameResponse) error, watchModes []string, sequenced bool, d *dispatch.Dispatcher, request FrameRequest, verified dispatch.VerifiedMessage) {
	if sequenced {
		baseWrite := write
		var sequence uint64
		write = func(response FrameResponse) error {
			response.Sequence = sequence + 1
			if err := baseWrite(response); err != nil {
				return err
			}
			sequence++
			return nil
		}
	}
	if d == nil {
		_ = writeCorrelatedFailure(write, request.CorrelationID, store.ErrInvalid)
		return
	}
	timeout := 4 * time.Second
	if request.Stream {
		timeout = 20 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if !request.Stream {
		result, err := d.DetachedVerified(ctx, verified, time.Now())
		if err != nil {
			_ = writeCorrelatedFailure(write, request.CorrelationID, err)
			return
		}
		if result.Capabilities != nil {
			result.Capabilities.MaxRequestFrameBytes = MaxRequestFrameBytes
			result.Capabilities.MaxResponseFrameBytes = MaxResponseFrameBytes
			if len(result.Capabilities.WatchModes) > 0 && len(watchModes) > 0 {
				result.Capabilities.WatchModes = append([]string(nil), watchModes...)
			}
		}
		if err = write(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "result", Result: &result}); errors.Is(err, store.ErrCapacity) {
			_ = writeCorrelatedFailure(write, request.CorrelationID, err)
		}
		return
	}
	started, bytes, frames := false, 0, 0
	err := d.DetachedWatchVerified(ctx, verified, time.Now(), func(page backend.HistoryPage) error {
		response := FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "page", Page: &page}
		if sequenced {
			response.Sequence = uint64(frames + 1)
		}
		body, err := json.Marshal(response)
		if err != nil {
			return store.ErrInvalid
		}
		if len(body) > MaxResponseFrameBytes || bytes+len(body)+4 > MaxStreamBytes-1024 || frames >= MaxStreamFrames {
			return store.ErrCapacity
		}
		if err = write(response); err != nil {
			return err
		}
		started = true
		bytes += len(body) + 4
		frames++
		return nil
	})
	if parent.Err() != nil {
		return
	}
	if !started {
		if err != nil {
			_ = writeCorrelatedFailure(write, request.CorrelationID, err)
		}
		return
	}
	reason := "resume"
	if errors.Is(err, backend.ErrStreamRenew) {
		reason = "lease_expired"
	} else if errors.Is(err, store.ErrCapacity) {
		reason = "capacity"
	} else if err != nil {
		reason = "upstream_unavailable"
	}
	_ = write(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "end", Reason: reason})
}

func writeCorrelatedFailure(write func(FrameResponse) error, id string, err error) error {
	status, code := httpError(err)
	return write(FrameResponse{Version: 1, CorrelationID: id, Event: "error", Error: &core.Failure{Code: code, Transient: status == 429 || status == 503}})
}
