package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

const MaxStreamBytes = 1 << 20
const MaxStreamFrames = 128

// Whole page events carry opaque resumable IDs. Keepalives never advance them.
// A fixed lease bounds authorization staleness; renewal authenticates again.
func httpEvents(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, token string) {
	body, start, err := readRequest(r)
	if err == nil && len(r.Header.Values("Last-Event-ID")) != 0 {
		ids := r.Header.Values("Last-Event-ID")
		var request core.Request
		if len(ids) != 1 || len(ids[0]) > 256 || ids[0] == "" || json.Unmarshal(body, &request) != nil || request.Cursor != "" && request.Cursor != ids[0] {
			err = store.ErrInvalid
		} else {
			request.Cursor = ids[0]
			body, err = json.Marshal(request)
		}
	}
	if err != nil {
		httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
		return
	}
	httpEventStream(w, r, func(ctx context.Context, emit func(backend.HistoryPage) error) error {
		_, err := d.ReadPage(ctx, token, body, start, time.Now(), emit)
		return err
	}, func(err error) {
		status, code := httpError(err)
		httpFailure(w, status, code)
	})
}

func httpDetachedEvents(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, encrypted bool) {
	body, ok := httpDetachedBody(w, r, encrypted)
	if !ok {
		return
	}
	httpEventStream(w, r, func(ctx context.Context, emit func(backend.HistoryPage) error) error {
		return d.DetachedWatch(ctx, string(body), encrypted, time.Now(), emit)
	}, func(err error) { httpDetachedFailure(w, err, encrypted) })
}

// Both authentication profiles share the same frame, write and lease bounds.
func httpEventStream(w http.ResponseWriter, r *http.Request, read func(context.Context, func(backend.HistoryPage) error) error, fail func(error)) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	control := http.NewResponseController(w)
	defer func() { _ = control.SetWriteDeadline(time.Now().Add(5 * time.Second)) }()
	started := false
	bytes, frames := 0, 0
	write := func(frame []byte) error {
		if len(frame) > backend.MaxHistoryBytes || bytes+len(frame) > MaxStreamBytes-1024 || frames >= MaxStreamFrames {
			return store.ErrCapacity
		}
		if err := control.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
			return err
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
			started = true
		}
		n, err := w.Write(frame)
		bytes += n
		frames++
		if err != nil {
			return err
		}
		return control.Flush()
	}
	emit := func(page backend.HistoryPage) error {
		if started && len(page.Messages) == 0 && !page.Gap && page.Status == "ok" {
			return write([]byte(": keepalive\n\n"))
		}
		data, err := json.Marshal(page)
		if err != nil {
			return store.ErrInvalid
		}
		frame := []byte("event: page\n")
		if !started {
			frame = append([]byte("retry: 1000\n"), frame...)
		}
		if page.Cursor != "" {
			frame = append(frame, []byte("id: "+page.Cursor+"\n")...)
		}
		frame = append(frame, []byte("data: ")...)
		frame = append(frame, data...)
		frame = append(frame, '\n', '\n')
		return write(frame)
	}
	err := read(ctx, emit)
	if !started {
		if err != nil {
			fail(err)
		}
		return
	}
	if r.Context().Err() != nil {
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
	// Reserved terminal budget; it has no ID and cannot advance the checkpoint.
	if control.SetWriteDeadline(time.Now().Add(3*time.Second)) == nil {
		_, _ = w.Write([]byte("event: end\ndata: {\"reason\":\"" + reason + "\"}\n\n"))
		_ = control.Flush()
	}
}
