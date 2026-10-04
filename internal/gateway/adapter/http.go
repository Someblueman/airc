package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
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
// never binds an address. The service owns configured lifecycle; richer HTTP
// full HTTP/catalogue acceptance remains pending.
func HTTPServer(d *dispatch.Dispatcher, slots int, allowedOrigins []string) (*http.Server, error) {
	if d == nil || slots < 1 || slots > 256 {
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
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if r.TLS == nil && !net.ParseIP(host).IsLoopback() {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		if !httpOrigin(w, r, origins) {
			return
		}
		readPage := r.Method == http.MethodGet && (r.URL.Path == "/v1/history" || r.URL.Path == "/v1/search" || r.URL.Path == "/v1/watch" || r.URL.Path == "/v1/events" || r.URL.Path == "/v1/directory" || r.URL.Path == "/v1/context")
		if r.URL.RawQuery != "" && !readPage || len(r.Header.Values("Authorization")) > 1 {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return
		}
		if r.Method == http.MethodPost && (r.URL.Path == "/v1/envelopes/signed" || r.URL.Path == "/v1/envelopes/encrypted") {
			httpDetached(w, r, d, r.URL.Path == "/v1/envelopes/encrypted")
			return
		}
		if r.Method == http.MethodPost && (r.URL.Path == "/v1/envelopes/signed/events" || r.URL.Path == "/v1/envelopes/encrypted/events") {
			httpDetachedEvents(w, r, d, r.URL.Path == "/v1/envelopes/encrypted/events")
			return
		}
		credential := r.Header.Get("Authorization")
		if !strings.HasPrefix(credential, "Bearer ") || len(credential) != 71 {
			httpFailure(w, http.StatusUnauthorized, core.AuthenticationFailed)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/events" {
			httpEvents(w, r, d, strings.TrimPrefix(credential, "Bearer "))
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/directory" {
			httpDirectory(w, r, d, strings.TrimPrefix(credential, "Bearer "))
			return
		}
		if readPage {
			if r.URL.Path == "/v1/context" {
				httpReadContext(w, r, d, strings.TrimPrefix(credential, "Bearer "))
			} else {
				httpReadPage(w, r, d, strings.TrimPrefix(credential, "Bearer "))
			}
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/capabilities" {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			capabilities, err := d.Capabilities(ctx, strings.TrimPrefix(credential, "Bearer "))
			if err != nil {
				status, code := httpError(err)
				httpFailure(w, status, code)
				return
			}
			_ = json.NewEncoder(w).Encode(capabilities)
			return
		}
		putID := requestResourceID(r.URL.Path)
		mutation := r.Method == http.MethodPost && r.URL.Path == "/v1/messages" || r.Method == http.MethodPut && putID != ""
		var body []byte
		if mutation {
			media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || media != "application/json" {
				httpFailure(w, http.StatusUnsupportedMediaType, core.InvalidRequest)
				return
			}
			body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, core.MaxEnvelopeBytes))
			if err != nil {
				var oversized *http.MaxBytesError
				status := http.StatusBadRequest
				if errors.As(err, &oversized) {
					status = http.StatusRequestEntityTooLarge
				}
				httpFailure(w, status, core.InvalidRequest)
				return
			}
			keys := r.Header.Values("Idempotency-Key")
			if r.Method == http.MethodPut || len(keys) != 0 {
				request, err := core.Decode(body)
				if err != nil {
					if r.Method == http.MethodPut {
						httpFailure(w, http.StatusBadRequest, core.InvalidRequest)
					} else {
						httpFailure(w, http.StatusConflict, core.IDConflict)
					}
					return
				}
				if r.Method == http.MethodPut && request.RequestID != putID || len(keys) != 0 && (len(keys) != 1 || keys[0] != request.RequestID) {
					httpFailure(w, http.StatusConflict, core.IDConflict)
					return
				}
			}

		} else if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/requests/") {
			id := strings.TrimPrefix(r.URL.Path, "/v1/requests/")
			body, _ = json.Marshal(core.Request{Version: 1, RequestID: "status", Operation: core.ReceiptGet, LookupID: id})
		} else {
			httpFailure(w, http.StatusNotFound, core.InvalidRequest)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var result core.Result
		var err error
		if mutation {
			result, err = d.Mutation(ctx, strings.TrimPrefix(credential, "Bearer "), body, time.Now())
		} else {
			result, err = d.Session(ctx, strings.TrimPrefix(credential, "Bearer "), body, time.Now())
		}
		if err != nil {
			status, code := httpError(err)
			httpFailure(w, status, code)
			return
		}
		status := http.StatusOK
		if mutation {
			status = http.StatusAccepted
			w.Header().Set("Location", "/v1/requests/"+result.RequestID)
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(result)
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &http.Server{Handler: h, Protocols: protocols, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func httpError(err error) (int, core.ErrorCode) {
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

func httpFailure(w http.ResponseWriter, status int, code core.ErrorCode) {
	if status == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="airc-gateway"`)
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Version int          `json:"version"`
		Error   core.Failure `json:"error"`
	}{1, core.Failure{Code: code, Transient: status == 429 || status == 503}})
}
