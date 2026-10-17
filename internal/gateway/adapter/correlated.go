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

// correlatedTransport is the small protocol boundary shared by framed,
// WebSocket, QUIC, WebRTC and datagram connections. The transport owns
// framing, deadlines, and closing; this loop owns validation, admission,
// dispatch, and stream budgets.
type correlatedTransport struct {
	next        func() ([]byte, error)
	write       func(FrameResponse) error
	watchModes  []string
	gracefulEOF bool
	sequenced   bool
	// plain is this connection's source and session: each frame's contract
	// request runs as its author. nil refuses every frame.
	plain *dispatch.Ingress
	// streaming, when set, tells the transport before each read whether a
	// request is still active on the connection, so a client that is only
	// reading a stream (a follow for minutes) is not dropped for sending
	// nothing; the stream's own writes notice a dead peer.
	streaming func(bool)
}

// ConnectionIdle is how long a connection carrier (framed, Unix, WebSocket)
// waits for a client's next frame between requests before closing; a
// connection with a request active, such as a follow, waits streamIdle
// instead, since the stream's own writes notice a dead peer. Tests shorten
// them; linkedStreamIdle is empty in release builds (core.LinkedDuration).
var (
	ConnectionIdle   = 30 * time.Second
	streamIdle       = core.LinkedDuration(linkedStreamIdle, time.Hour)
	linkedStreamIdle string
)

func runCorrelated(ctx context.Context, transport correlatedTransport, d *dispatch.Dispatcher, requests chan struct{}) error {
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
		if transport.streaming != nil {
			activeMu.Lock()
			transport.streaming(len(active) > 0)
			activeMu.Unlock()
		}
		body, err := transport.next()
		if err != nil {
			if transport.gracefulEOF && errors.Is(err, errFramedCleanEOF) {
				drain = true
				return nil
			}
			return err
		}
		request, plainBody, err := decodeFrame(body)
		if err != nil || request.Version != 1 || !correlationID(request.CorrelationID) {
			return store.ErrInvalid
		}
		// Correlation collisions are checked before profile validation, so a
		// rejected request can never alias an active stream.
		activeMu.Lock()
		duplicate := active[request.CorrelationID]
		activeMu.Unlock()
		if duplicate {
			return store.ErrInvalid
		}
		if request.Profile != "" && request.Profile != "plain" || plainBody == nil {
			if err := fail(request.CorrelationID, store.ErrInvalid); err != nil {
				return err
			}
			continue
		}
		if transport.plain == nil {
			if err := fail(request.CorrelationID, dispatch.ErrUnsupported); err != nil {
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
			runCorrelatedPlainRequest(ctx, transport.write, transport.watchModes, transport.sequenced, d, request, *transport.plain, plainBody)
		})
	}
}

// decodeFrame reads a frame and its contract request object: the same bytes
// a client sends on every other carrier.
func decodeFrame(body []byte) (FrameRequest, []byte, error) {
	var request FrameRequest
	if err := jsoninput.Validate(body, MaxRequestFrameBytes); err != nil {
		return request, nil, err
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil || members == nil {
		return request, nil, store.ErrInvalid
	}
	plain, ok := members["request"]
	if ok {
		if len(plain) == 0 || plain[0] != '{' {
			return request, nil, store.ErrInvalid
		}
		delete(members, "request")
		var err error
		if body, err = json.Marshal(members); err != nil {
			return request, nil, err
		}
	}
	if err := jsoninput.Decode(body, &request, MaxRequestFrameBytes); err != nil {
		return request, nil, err
	}
	return request, plain, nil
}

// runCorrelatedPlainRequest runs a plain contract request as its author
// (or the connection's identified author) and answers with one result frame,
// or page frames then an end frame.
func runCorrelatedPlainRequest(parent context.Context, write func(FrameResponse) error, watchModes []string, sequenced bool, d *dispatch.Dispatcher, request FrameRequest, in dispatch.Ingress, body []byte) {
	follow := false
	if request.Stream {
		// A streamed check is a follow: it runs for the follow lease, not a
		// watch's.
		if sub, err := core.DecodeSubmission(body); err == nil && sub.Request.Operation == core.Check {
			follow = true
		}
	}
	runCorrelatedExchange(parent, write, watchModes, sequenced, d, request, follow, func(ctx context.Context) (dispatch.DetachedResult, error) {
		return d.Submit(ctx, in, body, time.Now())
	}, func(ctx context.Context, sink dispatch.StreamSink) error {
		sub, err := core.DecodeSubmission(body)
		if err != nil {
			return store.ErrInvalid
		}
		return d.SubmitDecodedStream(ctx, in, sub, time.Now(), sink)
	})
}

// followLeaseMargin is added to a follow's lease for its stream deadline,
// so the lease ends the stream, not the carrier.
const followLeaseMargin = 5 * time.Second

func runCorrelatedExchange(parent context.Context, write func(FrameResponse) error, watchModes []string, sequenced bool, d *dispatch.Dispatcher, request FrameRequest, follow bool, once func(context.Context) (dispatch.DetachedResult, error), stream func(context.Context, dispatch.StreamSink) error) {
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
	if follow {
		timeout = d.FollowLease() + followLeaseMargin
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if !request.Stream {
		result, err := once(ctx)
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
	// A follow's frames are unbounded in number (its keepalives would
	// exhaust a watch's budget), bounded in bytes.
	emitFrame := func(response FrameResponse, counted bool) error {
		if sequenced {
			response.Sequence = uint64(frames + 1)
		}
		body, err := json.Marshal(response)
		if err != nil {
			return store.ErrInvalid
		}
		if len(body) > MaxResponseFrameBytes || bytes+len(body)+4 > MaxStreamBytes-1024 || counted && frames >= MaxStreamFrames {
			return store.ErrCapacity
		}
		response.encoded, response.encodedSequence = body, response.Sequence
		if err = write(response); err != nil {
			return err
		}
		started = true
		bytes += len(body) + 4
		frames++
		return nil
	}
	err := stream(ctx, dispatch.StreamSink{
		Page: func(page backend.HistoryPage) error {
			return emitFrame(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "page", Page: &page}, true)
		},
		Check: func(result core.CheckResult) error {
			return emitFrame(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "check", Check: &result}, false)
		},
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
	reason := watchEndReason(err)
	_ = write(FrameResponse{Version: 1, CorrelationID: request.CorrelationID, Event: "end", Reason: reason})
}

func writeCorrelatedFailure(write func(FrameResponse) error, id string, err error) error {
	_, failure := errorFailure(err)
	return write(FrameResponse{Version: 1, CorrelationID: id, Event: "error", Error: &failure})
}

// watchEndReason is why a watch stream ended: its lease expired, it reached
// a capacity budget, AIRC became unavailable, or the client should resume.
func watchEndReason(err error) string {
	switch {
	case errors.Is(err, backend.ErrStreamRenew):
		return "lease_expired"
	case errors.Is(err, store.ErrCapacity):
		return "capacity"
	case err != nil:
		return "upstream_unavailable"
	}
	return "resume"
}
