package fallback

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// proxyFor is the HTTP proxy for an http or websocket endpoint, or nil.
// Socket paths are never proxied.
func (c *Client) proxyFor(e Endpoint) (*url.URL, error) {
	if e.Path != "" {
		return nil, nil
	}
	_, address := c.address(e)
	target := &url.URL{Scheme: "http", Host: address}
	if e.TLS {
		target.Scheme = "https"
	}
	if c.Proxy != nil {
		return c.Proxy(target)
	}
	return http.ProxyFromEnvironment(&http.Request{URL: target})
}

// dialStream opens an http or websocket endpoint's byte stream: directly, or
// through the proxy's CONNECT tunnel when a proxy applies. TLS to the
// endpoint, when it uses TLS, is layered on top by the caller.
func (c *Client) dialStream(ctx context.Context, e Endpoint, progress func()) (net.Conn, error) {
	network, address := c.address(e)
	proxy, err := c.proxyFor(e)
	if err != nil {
		return nil, &net.OpError{Op: "proxyconnect", Net: "tcp", Err: err}
	}
	if proxy == nil {
		return c.dial(ctx, network, address, progress)
	}
	return c.tunnel(ctx, proxy, address, progress)
}

// tunnel asks an http or https proxy to CONNECT to address. Failures are
// net.OpErrors ("proxyconnect"), so a proxy that cannot be reached counts as
// an unreachable carrier and nothing was sent to the endpoint.
func (c *Client) tunnel(ctx context.Context, proxy *url.URL, address string, progress func()) (net.Conn, error) {
	fail := func(err error) error { return &net.OpError{Op: "proxyconnect", Net: "tcp", Err: err} }
	port := proxy.Port()
	switch proxy.Scheme {
	case "http":
		if port == "" {
			port = "80"
		}
	case "https":
		if port == "" {
			port = "443"
		}
	default:
		return nil, fail(fmt.Errorf("proxy scheme %q is not supported", proxy.Scheme))
	}
	conn, err := c.dial(ctx, "tcp", net.JoinHostPort(proxy.Hostname(), port), progress)
	if err != nil {
		return nil, err
	}
	if proxy.Scheme == "https" {
		if conn, err = handshake(ctx, conn, &tls.Config{ServerName: proxy.Hostname(), MinVersion: tls.VersionTLS12}); err != nil {
			return nil, fail(err)
		}
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: address}, Host: address, Header: http.Header{}}
	if user := proxy.User; user != nil {
		password, _ := user.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	reader := bufio.NewReader(conn)
	err = req.Write(conn)
	var resp *http.Response
	if err == nil {
		resp, err = http.ReadResponse(reader, req)
	}
	if !stop() && err == nil {
		err = ctx.Err()
	}
	_ = conn.SetDeadline(time.Time{})
	switch {
	case err != nil:
	case resp.StatusCode != http.StatusOK:
		err = fmt.Errorf("proxy refused CONNECT: %s", resp.Status)
	case reader.Buffered() > 0:
		err = errors.New("proxy sent data before the tunnel opened")
	}
	if err != nil {
		_ = conn.Close()
		return nil, fail(err)
	}
	return conn, nil
}
