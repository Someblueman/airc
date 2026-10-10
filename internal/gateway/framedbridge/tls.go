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
	correlation, err := Correlation(request)
	if err != nil {
		return nil, err
	}
	ca, err := os.Open(o.CA)
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
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: o.ServerName, NextProtos: []string{"airc-gateway/1"},
	}}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, err := dialer.DialContext(dialCtx, "tcp", o.Address)
	cancel()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if conn.(*tls.Conn).ConnectionState().NegotiatedProtocol != "airc-gateway/1" {
		return nil, errors.New("gateway TLS ALPN mismatch")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("gateway operation requires deadline")
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
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
