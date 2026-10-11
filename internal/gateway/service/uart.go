package service

import (
	"context"
	"crypto/tls"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// RunUART owns one serial session. With certificates, TLS protects replies as
// well as requests; without, plain author requests travel in the clear.
// Link loss or malformed framing ends the session instead of silently reopening.
func RunUART(ctx context.Context, c config.Config) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	var listener config.Listener
	count := 0
	for _, l := range c.Listeners {
		if l.Enabled {
			count++
			if l.Protocol == "uart" {
				listener = l
			}
		}
	}
	if count != 1 || listener.Protocol != "uart" {
		return errors.New("serve-uart requires exactly one enabled uart listener")
	}
	if listener.TLSCertFile == "" {
		// No TLS: plain author requests in the clear on the serial line.
		stream, err := adapter.OpenUART(listener.Address)
		if err != nil {
			return err
		}
		defer func() { failure = errors.Join(failure, stream.Close()) }()
		return runLocalStream(ctx, c, stream, "uart")
	}
	pair, err := auth.ServerCertificate(listener.TLSCertFile, listener.TLSKeyFile)
	if err != nil {
		return err
	}
	stream, err := adapter.OpenUART(listener.Address)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, stream.Close()) }()
	secure := tls.Server(stream, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}, NextProtos: []string{adapter.FramedALPN}})
	return runLocalStream(ctx, c, secure, "uart")
}
