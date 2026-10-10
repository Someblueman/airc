package adapter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// GitHubWebhookServer accepts one bounded provider event per HTTPS request.
// The service supplies the TLS listener and joins handlers before clearing keys.
func GitHubWebhookServer(d *dispatch.Dispatcher, verifier *auth.GitHubWebhook, requests chan struct{}) (*http.Server, error) {
	if d == nil || verifier == nil || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.TLS == nil || r.URL.Path != "/v1/github/events" || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 0 {
			http.Error(w, "request refused", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method refused", http.StatusMethodNotAllowed)
			return
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			http.Error(w, "request capacity exceeded", http.StatusTooManyRequests)
			return
		}
		if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || len(r.Header.Values("X-GitHub-Event")) != 1 || len(r.Header.Values("X-Hub-Signature-256")) != 1 {
			http.Error(w, "request refused", http.StatusBadRequest)
			return
		}
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/json" || len(params) != 0 && (len(params) != 1 || params["charset"] != "utf-8") {
			http.Error(w, "content type refused", http.StatusUnsupportedMediaType)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, auth.MaxGitHubWebhookBytes))
		if err != nil {
			status := http.StatusBadRequest
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "body refused", status)
			return
		}
		if r.Header.Get("X-GitHub-Event") == "ping" {
			if verifier.VerifyPing(r.Header.Get("X-Hub-Signature-256"), body) != nil {
				http.Error(w, "event refused", http.StatusUnauthorized)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		proof, err := verifier.Verify(r.Header.Get("X-GitHub-Event"), r.Header.Get("X-Hub-Signature-256"), body, time.Now())
		if err != nil {
			http.Error(w, "event refused", http.StatusUnauthorized)
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		if proof.ReplayDeadline().Before(deadline) {
			deadline = proof.ReplayDeadline()
		}
		ctx, cancel := context.WithDeadline(r.Context(), deadline)
		defer cancel()
		result, err := d.GitHub(ctx, proof, time.Now())
		if err != nil {
			status, _ := httpError(err)
			http.Error(w, "event admission refused", status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(result)
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &http.Server{Handler: handler, Protocols: protocols, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}
