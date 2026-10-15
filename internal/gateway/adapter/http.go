package adapter

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// HTTPServer owns protocol deadlines and a nonblocking ingress slot bound.
// The caller supplies an explicit listener, TLS and lifecycle; this constructor
// never binds an address.
func HTTPServer(d *dispatch.Dispatcher, slots int, allowedOrigins []string) (*http.Server, error) {
	if d == nil || !d.AuthorAccess() || slots < 1 || slots > config.MaxRequests {
		return nil, store.ErrInvalid
	}
	if err := config.ValidateOrigins(allowedOrigins); err != nil {
		return nil, err
	}
	origins := make(map[string]bool, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = true
	}
	gate := make(chan struct{}, slots)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Add("Vary", "Origin")
		if values := r.Header.Values("Origin"); len(values) == 1 && origins[values[0]] {
			w.Header().Set("Access-Control-Allow-Origin", values[0])
			w.Header().Set("Access-Control-Expose-Headers", "Location, WWW-Authenticate")
		}

		select {
		case gate <- struct{}{}:
			defer func() { <-gate }()
		default:
			httpFailure(w, http.StatusTooManyRequests, core.CapacityExceeded)
			return
		}
		if !httpOrigin(w, r, origins) {
			return
		}
		readPage := r.Method == http.MethodGet && readPaths[r.URL.Path]
		if r.URL.RawQuery != "" && !readPage {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		// Every request is a plain contract request run as the author it
		// names. Like IRC, it needs no TLS and no gateway credential.
		httpAuthor(w, r, d, readPage)
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{Handler: h, Protocols: protocols, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func httpError(err error) (int, core.ErrorCode) {
	status, failure := errorFailure(err)
	return status, failure.Code
}

// errorFailure maps an error to its HTTP status and stable failure, keeping
// AIRC's own refusal (and its numeric) for synchronous account calls.
func errorFailure(err error) (int, core.Failure) {
	var refusal *backend.Refusal
	if errors.As(err, &refusal) {
		status := http.StatusBadGateway
		switch refusal.Failure.Code {
		case core.AuthenticationFailed:
			status = http.StatusUnauthorized
		case core.NickRegistered, core.NickInUse:
			status = http.StatusConflict
		case core.CapacityExceeded:
			status = http.StatusTooManyRequests
		case core.UnsupportedOperation:
			status = http.StatusNotImplemented
		case core.UpstreamUnavailable:
			status = http.StatusServiceUnavailable
		}
		return status, refusal.Failure
	}
	status, code := plainHTTPError(err)
	return status, core.Failure{Code: code, Transient: status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable}
}

func plainHTTPError(err error) (int, core.ErrorCode) {
	switch {
	case errors.Is(err, auth.ErrAuthentication):
		return http.StatusUnauthorized, core.AuthenticationFailed
	case errors.Is(err, auth.ErrForbidden):
		return http.StatusForbidden, core.Forbidden
	case errors.Is(err, dispatch.ErrUnsupported), errors.Is(err, backend.ErrReadUnsupported):
		return http.StatusNotImplemented, core.UnsupportedOperation
	case errors.Is(err, store.ErrConflict):
		return http.StatusConflict, core.IDConflict
	case errors.Is(err, store.ErrCapacity):
		return http.StatusTooManyRequests, core.CapacityExceeded
	case errors.Is(err, store.ErrInvalid):
		return http.StatusBadRequest, core.InvalidRequest
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound, core.InvalidRequest
	default:
		return http.StatusServiceUnavailable, core.UpstreamUnavailable
	}
}

// httpChallenge is the 401 challenge: the account token AIRC refused belongs
// in the request's author (or the AIRC-Token header).
const httpChallenge = `AIRC realm="airc-gateway"`

func httpFailure(w http.ResponseWriter, status int, code core.ErrorCode) {
	if status == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", httpChallenge)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Version int          `json:"version"`
		Error   core.Failure `json:"error"`
	}{1, core.Failure{Code: code, Transient: status == 429 || status == 503}})
}

// httpFailureErr writes err's failure, including AIRC's numeric when known.
func httpFailureErr(w http.ResponseWriter, err error) {
	status, failure := errorFailure(err)
	if status == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", httpChallenge)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Version int          `json:"version"`
		Error   core.Failure `json:"error"`
	}{1, failure})
}
