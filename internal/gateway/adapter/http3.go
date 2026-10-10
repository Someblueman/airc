package adapter

import (
	"context"
	"crypto/tls"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
)

const (
	http3HandshakeIdle = 5 * time.Second
	http3IdleTimeout   = 30 * time.Second
	http3UniStreams    = 16
	http3RecvWindow    = uint64(64 << 10)
)

// HTTP3Server creates the HTTP/3 transport using the existing HTTP handler
// tree. Listener, certificate assignment, and service lifecycle remain owned
// by the caller.
func HTTP3Server(d *dispatch.Dispatcher, slots int, origins []string) (*http3.Server, error) {
	if d == nil || slots < 1 || slots > 256 {
		return nil, store.ErrInvalid
	}
	base, err := HTTPServer(d, slots, origins)
	if err != nil {
		return nil, err
	}
	tlsConfig := base.TLSConfig.Clone()
	tlsConfig.MinVersion = tls.VersionTLS13
	tlsConfig.NextProtos = []string{"h3"}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || !r.TLS.HandshakeComplete || r.ProtoMajor != 3 {
			w.WriteHeader(http.StatusTooEarly)
			return
		}
		controller := http.NewResponseController(w)
		if controller.SetReadDeadline(time.Now().Add(5*time.Second)) != nil || controller.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 23*time.Second)
		defer cancel()
		base.Handler.ServeHTTP(w, r.WithContext(ctx))
	})
	return &http3.Server{
		Handler:   h,
		TLSConfig: tlsConfig,
		QUICConfig: &quic.Config{
			Allow0RTT:                      false,
			HandshakeIdleTimeout:           http3HandshakeIdle,
			MaxIdleTimeout:                 http3IdleTimeout,
			MaxIncomingStreams:             int64(slots),
			MaxIncomingUniStreams:          http3UniStreams,
			InitialStreamReceiveWindow:     http3RecvWindow,
			MaxStreamReceiveWindow:         http3RecvWindow,
			InitialConnectionReceiveWindow: http3RecvWindow,
			MaxConnectionReceiveWindow:     http3RecvWindow,
		},
		MaxHeaderBytes: 16 << 10,
		IdleTimeout:    http3IdleTimeout,
	}, nil
}
