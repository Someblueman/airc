package auth

import (
	"context"
	"crypto/tls"
	"io"
	"mime"
	"net"
	"net/http"
	"time"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

// getMatrixBody returns bounded bytes from one fixed-endpoint verified request.
// The caller owns clearing the returned body and interpreting its schema.
func (s *MatrixServer) getMatrixBody(parent context.Context, path string, maxBytes int) ([]byte, error) {
	if s == nil || s.security == nil || parent == nil || len(s.token) == 0 || maxBytes < 1 || maxBytes > 65536 {
		return nil, ErrAuthentication
	}
	if !matrixHTTPPath(path) {
		return nil, ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	_, port, err := net.SplitHostPort(s.address)
	if err != nil {
		return nil, ErrAuthentication
	}
	authority := net.JoinHostPort(s.security.ServerName, port)
	transport := &http.Transport{TLSClientConfig: s.security.Clone(), DisableKeepAlives: true, DisableCompression: true, MaxResponseHeaderBytes: 8192,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != authority {
				return nil, ErrAuthentication
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", s.address)
		}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+authority+path, nil)
	if err != nil {
		return nil, ErrAuthentication
	}
	req.Header.Set("Authorization", "Bearer "+string(s.token))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, ErrAuthentication
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.TLS == nil || resp.TLS.Version != tls.VersionTLS13 || len(resp.TLS.VerifiedChains) == 0 || resp.Header.Get("Content-Encoding") != "" {
		return nil, ErrAuthentication
	}
	media, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return nil, ErrAuthentication
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)+1))
	if err != nil {
		clear(data)
		return nil, ErrAuthentication
	}
	if len(data) > maxBytes || ctx.Err() != nil {
		clear(data)
		return nil, ErrAuthentication
	}
	return data, nil
}

func (s *MatrixServer) getMatrixJSON(parent context.Context, path string, dst any, maxBytes int) error {
	if parent == nil {
		return ErrAuthentication
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	data, err := s.getMatrixBody(ctx, path, maxBytes)
	defer clear(data)
	if err != nil || jsoninput.Decode(data, dst, maxBytes) != nil || ctx.Err() != nil {
		return ErrAuthentication
	}
	return nil
}
