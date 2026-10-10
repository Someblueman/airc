package couchdbwire

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	maxPageBytes = 512 << 10
	maxHeaders   = 8 << 10
)

var ErrRequest = errors.New("couchdb request failed")

type Client struct {
	endpoint  string
	database  string
	username  string
	password  []byte
	mu        sync.RWMutex
	closed    bool
	http      *http.Client
	transport *http.Transport
}

func ValidateLocation(endpoint, database string) error {
	if len(endpoint) == 0 || len(endpoint) > 320 || len(database) < 1 || len(database) > 64 {
		return ErrRequest
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || u.ForceQuery || u.Hostname() == "" || !validHost(u.Hostname()) {
		return ErrRequest
	}
	if port := u.Port(); port != "" {
		if !canonicalPort(port) || port == "443" {
			return ErrRequest
		}
	} else if strings.Contains(u.Host, ":") {
		return ErrRequest
	}
	if endpoint != "https://"+u.Host || strings.ToLower(u.Host) != u.Host {
		return ErrRequest
	}
	if database[0] < 'a' || database[0] > 'z' {
		return ErrRequest
	}
	for i := 1; i < len(database); i++ {
		b := database[i]
		if !(b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || strings.ContainsRune("_$()+-", rune(b))) {
			return ErrRequest
		}
	}
	return nil
}

func NewClient(endpoint, database, username string, password []byte, roots *x509.CertPool) (*Client, error) {
	if ValidateLocation(endpoint, database) != nil || !validUsername(username) || len(password) < 32 || len(password) > 256 {
		return nil, ErrRequest
	}
	for _, b := range password {
		if b < 0x21 || b > 0x7e {
			return nil, ErrRequest
		}
	}
	parsed, _ := url.Parse(endpoint)
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: parsed.Hostname()}
	if roots != nil {
		tlsConfig.RootCAs = roots.Clone()
	}
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, ForceAttemptHTTP2: false,
		TLSClientConfig: tlsConfig, DialContext: (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 8 * time.Second,
		MaxResponseHeaderBytes: maxHeaders, MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2,
		IdleConnTimeout: 20 * time.Second, TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &Client{endpoint: endpoint, database: database, username: username, password: bytes.Clone(password), transport: transport,
		http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func validUsername(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := range s {
		b := s[i]
		if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-') {
			return false
		}
	}
	return true
}

func (c *Client) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	clear(c.password)
	c.password = nil
	c.transport.CloseIdleConnections()
}

func (c *Client) Changes(ctx context.Context, since string) (Page, error) {
	if c == nil || ctx == nil || !ValidSequence(since) {
		return Page{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return Page{}, ErrRequest
	}
	query := url.Values{
		"feed": {"normal"}, "include_docs": {"true"}, "style": {"all_docs"}, "conflicts": {"true"},
		"attachments": {"false"}, "descending": {"false"}, "seq_interval": {"1"}, "limit": {"16"},
	}
	if since != "" {
		query.Set("since", since)
	}
	path := c.endpoint + "/" + url.PathEscape(c.database) + "/_changes?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return Page{}, ErrRequest
	}
	req.SetBasicAuth(c.username, string(c.password))
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return Page{}, ErrRequest
	}
	defer response.Body.Close()
	if ctx.Err() != nil || response.StatusCode != http.StatusOK || hasHeader(response.Header, "Content-Encoding") {
		return Page{}, ErrRequest
	}
	contentType, single := singleHeader(response.Header, "Content-Type")
	mediaType, params, err := mime.ParseMediaType(contentType)
	if !single || err != nil || mediaType != "application/json" || len(params) > 1 || len(params) == 1 && (params["charset"] == "" || !strings.EqualFold(params["charset"], "utf-8")) {
		return Page{}, ErrRequest
	}
	if response.ContentLength > maxPageBytes {
		return Page{}, ErrRequest
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxPageBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxPageBytes || response.ContentLength >= 0 && int64(len(body)) != response.ContentLength || ctx.Err() != nil {
		clear(body)
		return Page{}, ErrRequest
	}
	page, err := parsePage(body)
	clear(body)
	if err != nil || ctx.Err() != nil {
		return Page{}, ErrRequest
	}
	return page, nil
}

func hasHeader(h http.Header, name string) bool {
	for key := range h {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func singleHeader(h http.Header, name string) (string, bool) {
	var values []string
	for key, entries := range h {
		if strings.EqualFold(key, name) {
			values = append(values, entries...)
		}
	}
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}
