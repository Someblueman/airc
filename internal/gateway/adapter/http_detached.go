package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func httpDetached(w http.ResponseWriter, r *http.Request, d *dispatch.Dispatcher, encrypted bool) {
	body, ok := httpDetachedBody(w, r, encrypted)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	result, err := d.Detached(ctx, string(body), encrypted, time.Now())
	if err != nil {
		httpDetachedFailure(w, err, encrypted)
		return
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		httpFailure(w, 503, core.UpstreamUnavailable)
		return
	}
	if len(encoded)+1 > backend.MaxHistoryBytes {
		httpFailure(w, 429, core.CapacityExceeded)
		return
	}
	status := 200
	if result.Operation == core.Send || result.Operation == core.Reply || result.Operation == core.ReactionAdd {
		status = 202
		w.Header().Set("Location", "/v1/requests/"+result.Result.RequestID)
	}
	w.WriteHeader(status)
	_, _ = w.Write(append(encoded, '\n'))
}

func httpDetachedBody(w http.ResponseWriter, r *http.Request, encrypted bool) ([]byte, bool) {
	// The signed sender is the sole authority; never combine credentials or let
	// an unsigned header supply an operation ID or a cursor for this profile.
	if len(r.Header.Values("Authorization")) != 0 || len(r.Header.Values("Idempotency-Key")) != 0 || len(r.Header.Values("Last-Event-ID")) != 0 {
		httpFailure(w, 400, core.InvalidRequest)
		return nil, false
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/jose" {
		httpFailure(w, 415, core.InvalidRequest)
		return nil, false
	}
	max := int64(auth.MaxSignedBytes)
	if encrypted {
		max = auth.MaxEncryptedBytes
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	if err != nil {
		var oversized *http.MaxBytesError
		status := 400
		if errors.As(err, &oversized) {
			status = 413
		}
		httpFailure(w, status, core.InvalidRequest)
		return nil, false
	}
	return body, true
}

func httpDetachedFailure(w http.ResponseWriter, err error, encrypted bool) {
	status, code := httpError(err)
	if status == http.StatusUnauthorized {
		scheme := "AIRC-JWS"
		if encrypted {
			scheme = "AIRC-JWE"
		}
		w.Header().Set("WWW-Authenticate", scheme+` realm="airc-gateway"`)
	}
	httpFailure(w, status, code)
}
