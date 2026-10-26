package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/store"
)

const MaxStreamBytes = 1 << 20
const MaxStreamFrames = 128

// httpEventStream owns the frame, write and lease bounds of a watch stream.
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
		if started && len(page.Messages) == 0 && len(page.Events) == 0 && !page.Gap && page.Status == "ok" {
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
	reason := watchEndReason(err)
	// Reserved terminal budget; it has no ID and cannot advance the checkpoint.
	if control.SetWriteDeadline(time.Now().Add(3*time.Second)) == nil {
		_, _ = w.Write([]byte("event: end\ndata: {\"reason\":\"" + reason + "\"}\n\n"))
		_ = control.Flush()
	}
}
