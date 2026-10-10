package adapter

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// TwilioSMSServer accepts one signed inbound SMS event over HTTPS. The service
// owns the listener and joins handlers before closing the verifier.
func TwilioSMSServer(d *dispatch.Dispatcher, verifier *auth.TwilioSMS, requests chan struct{}) (*http.Server, error) {
	if d == nil || verifier == nil || cap(requests) < 1 {
		return nil, store.ErrInvalid
	}
	webhookURL, err := url.Parse(verifier.WebhookURL())
	if err != nil || webhookURL.Scheme != "https" || webhookURL.Host == "" || webhookURL.Host != webhookURL.Hostname() || webhookURL.User != nil || webhookURL.Port() != "" ||
		webhookURL.Hostname() != strings.ToLower(webhookURL.Hostname()) || strings.HasSuffix(webhookURL.Hostname(), ".") ||
		net.ParseIP(webhookURL.Hostname()) != nil ||
		webhookURL.Path != "/v1/twilio/sms" || webhookURL.RawPath != "" ||
		webhookURL.RawQuery != "" || webhookURL.ForceQuery || webhookURL.Fragment != "" || webhookURL.RawFragment != "" {
		return nil, store.ErrInvalid
	}
	expectedHost := webhookURL.Host
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.TLS == nil || r.Host != expectedHost || r.RequestURI != "/v1/twilio/sms" || r.URL.Path != "/v1/twilio/sms" || r.URL.RawPath != "" || r.URL.Opaque != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || len(r.Header.Values("Origin")) != 0 {
			twilioSMSFailure(w, http.StatusForbidden, "request refused")
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			twilioSMSFailure(w, http.StatusMethodNotAllowed, "method refused")
			return
		}
		select {
		case requests <- struct{}{}:
			defer func() { <-requests }()
		default:
			twilioSMSFailure(w, http.StatusTooManyRequests, "request capacity exceeded")
			return
		}
		if len(r.Header.Values("Content-Type")) != 1 || len(r.Header.Values("Content-Encoding")) != 0 || len(r.Header.Values("X-Twilio-Signature")) != 1 {
			twilioSMSFailure(w, http.StatusBadRequest, "request refused")
			return
		}
		media, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || media != "application/x-www-form-urlencoded" || len(params) != 0 && (len(params) != 1 || !strings.EqualFold(params["charset"], "utf-8")) {
			twilioSMSFailure(w, http.StatusUnsupportedMediaType, "content type refused")
			return
		}
		body, err := readTwilioSMSBody(w, r)
		if err != nil {
			clear(body)
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				twilioSMSFailure(w, http.StatusRequestEntityTooLarge, "body refused")
			} else {
				twilioSMSFailure(w, http.StatusBadRequest, "body refused")
			}
			return
		}
		defer clear(body)
		proof, err := verifier.Verify(r.Header.Get("X-Twilio-Signature"), body)
		if err != nil {
			twilioSMSFailure(w, http.StatusUnauthorized, "event refused")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		result, err := d.TwilioSMS(ctx, proof, time.Now())
		if err != nil {
			status, _ := httpError(err)
			twilioSMSFailure(w, status, "event admission refused")
			return
		}
		if !result.Durable {
			twilioSMSFailure(w, http.StatusServiceUnavailable, "event admission unavailable")
			return
		}
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<Response/>"))
	})
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &http.Server{Handler: handler, Protocols: protocols, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10}, nil
}

func readTwilioSMSBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	return io.ReadAll(http.MaxBytesReader(w, r.Body, auth.MaxTwilioSMSWebhookBytes))
}

func twilioSMSFailure(w http.ResponseWriter, status int, message string) {
	http.Error(w, message, status)
}
