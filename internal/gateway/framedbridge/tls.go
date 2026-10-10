package framedbridge

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// TLS specifies the fixed authenticated upstream gateway.
type TLS struct{ Address, CA, ServerName string }

func (o TLS) Validate() error {
	host, port, err := net.SplitHostPort(o.Address)
	ip := net.ParseIP(host)
	portNumber, portErr := strconv.ParseUint(port, 10, 16)
	if err != nil || ip == nil || ip.IsUnspecified() || ip.IsMulticast() || portErr != nil || portNumber == 0 || o.ServerName == "" || !filepath.IsAbs(o.CA) {
		return errors.New("bridge requires fixed numeric TLS address, certificate name and absolute CA path")
	}
	return nil
}

// Forward preserves the original request and returns a whole correlated result.
func Forward(ctx context.Context, o TLS, request []byte) ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}
	config, err := clientConfig(o.CA, o.ServerName)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", o.Address)
	if err != nil {
		return nil, err
	}
	return exchangeTLS(ctx, dialCtx, raw, config, request)
}

// ForwardConn owns raw and performs the same authenticated TLS exchange over a
// caller-established stream, including non-IP transports. It always closes raw.
func ForwardConn(ctx context.Context, raw net.Conn, caPath, serverName string, request []byte) ([]byte, error) {
	if raw == nil {
		return nil, errors.New("gateway stream is required")
	}
	config, err := clientConfig(caPath, serverName)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	return exchangeTLS(ctx, handshakeCtx, raw, config, request)
}

func clientConfig(caPath, serverName string) (*tls.Config, error) {
	if !filepath.IsAbs(caPath) || serverName == "" {
		return nil, errors.New("gateway requires absolute CA path and certificate name")
	}
	ca, err := os.Open(caPath)
	if err != nil {
		return nil, err
	}
	info, statErr := ca.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		ca.Close()
		return nil, errors.New("CA file must be bounded and regular")
	}
	pem, readErr := io.ReadAll(io.LimitReader(ca, (1<<20)+1))
	err = errors.Join(readErr, ca.Close())
	if err != nil || len(pem) > 1<<20 {
		return nil, errors.New("CA file read failed or exceeds bound")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid gateway CA")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots,
		ServerName: serverName, NextProtos: []string{"airc-gateway/1"}}, nil
}

func exchangeTLS(ctx, handshakeCtx context.Context, raw net.Conn, config *tls.Config, request []byte) ([]byte, error) {
	defer raw.Close()
	correlation, err := Correlation(request)
	if err != nil {
		return nil, err
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("gateway operation requires deadline")
	}
	if err := raw.SetDeadline(deadline); err != nil {
		return nil, err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = raw.SetDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	conn := tls.Client(raw, config)
	// Closing TLS before the cancellation callback is retired keeps teardown
	// interruptible. The underlying stream is closed even after handshake failure.
	defer conn.Close()
	err = conn.HandshakeContext(handshakeCtx)
	if err != nil {
		return nil, err
	}
	if conn.ConnectionState().NegotiatedProtocol != "airc-gateway/1" {
		return nil, errors.New("gateway TLS ALPN mismatch")
	}
	if err := WriteFrame(conn, request, MaxRequest); err != nil {
		return nil, err
	}
	response, err := ReadFrame(conn, MaxResponse)
	if err != nil {
		return nil, err
	}
	if err := ValidateResponse(response, correlation); err != nil {
		return nil, err
	}
	return response, nil
}
