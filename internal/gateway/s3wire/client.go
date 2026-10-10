package s3wire

import (
	"bytes"
	"context"
	"crypto/tls"
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
	maxRequestBytes  = 16 << 10
	maxResultBytes   = 64 << 10
	maxResponseBytes = 8 << 10
)

var (
	ErrRequest  = errors.New("s3 request failed")
	ErrNotExist = errors.New("s3 object does not exist")
	ErrConflict = errors.New("s3 object condition conflict")
)

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey []byte
	SessionToken    []byte
}

type Object struct {
	Size int64
	ETag string
}

type Client struct {
	region  string
	bucket  string
	account string
	prefix  string
	host    string
	access  string
	secret  []byte
	token   []byte
	mu      sync.RWMutex
	closed  bool
	http    *http.Client
	trans   *http.Transport
}

func NewClient(region, bucket, account, prefix string, creds Credentials) (*Client, error) {
	if ValidateLocation(region, bucket, account, prefix) != nil || !validCredentials(creds) {
		return nil, ErrRequest
	}
	host := bucket + ".s3." + region + ".amazonaws.com"
	transport := &http.Transport{
		Proxy: nil, DisableCompression: true, ForceAttemptHTTP2: false,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, ServerName: host},
		DialContext:         (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 10 * time.Second,
		MaxResponseHeaderBytes: 8192, MaxConnsPerHost: 2, MaxIdleConns: 2,
		MaxIdleConnsPerHost: 2, IdleConnTimeout: 30 * time.Second,
		TLSNextProto: make(map[string]func(string, *tls.Conn) http.RoundTripper),
	}
	return &Client{
		region: region, bucket: bucket, account: account, prefix: prefix, host: host,
		access: creds.AccessKeyID, secret: bytes.Clone(creds.SecretAccessKey), token: bytes.Clone(creds.SessionToken),
		trans: transport, http: &http.Client{Transport: transport, Timeout: 15 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}, nil
}

func validCredentials(c Credentials) bool {
	if len(c.AccessKeyID) < 16 || len(c.AccessKeyID) > 128 || len(c.SecretAccessKey) < 32 || len(c.SecretAccessKey) > 128 || len(c.SessionToken) > 4096 {
		return false
	}
	for i := 0; i < len(c.AccessKeyID); i++ {
		b := c.AccessKeyID[i]
		if !(b >= 'A' && b <= 'Z' || b >= '0' && b <= '9') {
			return false
		}
	}
	return printableCredential(c.SecretAccessKey, false) && printableCredential(c.SessionToken, true)
}

func printableCredential(value []byte, emptyOK bool) bool {
	if len(value) == 0 {
		return emptyOK
	}
	for _, b := range value {
		if b < 0x21 || b > 0x7e {
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
	if !c.closed {
		c.closed = true
		clear(c.secret)
		clear(c.token)
		c.secret, c.token = nil, nil
		c.trans.CloseIdleConnections()
	}
	c.mu.Unlock()
}

func (c *Client) Head(ctx context.Context, name string) (Object, error) {
	if c == nil || ctx == nil || !validObjectName(name) {
		return Object{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return Object{}, ErrRequest
	}
	response, err := c.request(ctx, http.MethodHead, name, nil, nil, "")
	if err != nil {
		return Object{}, ErrRequest
	}
	defer response.Body.Close()
	if ctx.Err() != nil {
		return Object{}, ErrRequest
	}
	if hasDeleteMarker(response.Header) {
		return Object{}, ErrRequest
	}
	if response.StatusCode == http.StatusNotFound {
		return Object{}, ErrNotExist
	}
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		return Object{}, ErrConflict
	}
	if response.StatusCode != http.StatusOK {
		return Object{}, ErrRequest
	}
	object, err := objectHeaders(response)
	if err != nil || ctx.Err() != nil {
		return Object{}, ErrRequest
	}
	return object, nil
}

func (c *Client) GetRequest(ctx context.Context, etag string) ([]byte, Object, error) {
	if c == nil || ctx == nil || !validETag(etag) {
		return nil, Object{}, ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return nil, Object{}, ErrRequest
	}
	response, err := c.request(ctx, http.MethodGet, "request.jwe", map[string]string{"If-Match": etag}, nil, "")
	if err != nil {
		return nil, Object{}, ErrRequest
	}
	defer response.Body.Close()
	if ctx.Err() != nil {
		return nil, Object{}, ErrRequest
	}
	if hasDeleteMarker(response.Header) {
		return nil, Object{}, ErrRequest
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, Object{}, ErrNotExist
	}
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		return nil, Object{}, ErrConflict
	}
	if response.StatusCode != http.StatusOK {
		return nil, Object{}, ErrRequest
	}
	object, err := objectHeaders(response)
	if err != nil {
		return nil, Object{}, ErrRequest
	}
	if object.ETag != etag {
		return nil, Object{}, ErrConflict
	}
	if response.ContentLength <= 0 || response.ContentLength > maxRequestBytes {
		return nil, Object{}, ErrRequest
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxRequestBytes+1))
	if err != nil || len(body) == 0 || len(body) > maxRequestBytes || int64(len(body)) != response.ContentLength || ctx.Err() != nil {
		clear(body)
		return nil, Object{}, ErrRequest
	}
	return body, object, nil
}

func (c *Client) PutResult(ctx context.Context, body []byte) error {
	if c == nil || ctx == nil || len(body) == 0 || len(body) > maxResultBytes {
		return ErrRequest
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.closed {
		return ErrRequest
	}
	response, err := c.request(ctx, http.MethodPut, "result.jwe", map[string]string{"If-None-Match": "*"}, body, "application/jose")
	if err != nil {
		return ErrRequest
	}
	defer response.Body.Close()
	if ctx.Err() != nil {
		return ErrRequest
	}
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		return ErrConflict
	}
	if response.StatusCode != http.StatusOK || hasDeleteMarker(response.Header) || len(response.Header.Values("Content-Encoding")) != 0 {
		return ErrRequest
	}
	etag, ok := oneHeader(response.Header, "ETag")
	if !ok || !validETag(etag) || response.ContentLength > maxResponseBytes {
		return ErrRequest
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(responseBody) > maxResponseBytes || ctx.Err() != nil {
		clear(responseBody)
		return ErrRequest
	}
	clear(responseBody)
	return nil
}

func (c *Client) request(ctx context.Context, method, name string, conditional map[string]string, body []byte, contentType string) (*http.Response, error) {
	if ctx.Err() != nil || len(c.secret) == 0 {
		return nil, ErrRequest
	}
	uri := &url.URL{Scheme: "https", Host: c.host, Path: "/" + c.prefix + "/" + name}
	request, err := http.NewRequestWithContext(ctx, method, uri.String(), bytes.NewReader(body))
	if err != nil {
		return nil, ErrRequest
	}
	request.Host = c.host
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("X-Amz-Content-Sha256", payloadHash(body))
	request.Header.Set("X-Amz-Expected-Bucket-Owner", c.account)
	if len(c.token) != 0 {
		request.Header.Set("X-Amz-Security-Token", string(c.token))
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	for key, value := range conditional {
		request.Header.Set(key, value)
	}
	if err := signRequest(request, c.region, c.access, c.secret, time.Now().UTC()); err != nil {
		return nil, ErrRequest
	}
	response, err := c.http.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, ErrRequest
	}
	return response, nil
}

func validObjectName(name string) bool { return name == "request.jwe" || name == "result.jwe" }

func objectHeaders(response *http.Response) (Object, error) {
	if len(response.Header.Values("Content-Encoding")) != 0 {
		return Object{}, ErrRequest
	}
	contentTypes := response.Header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return Object{}, ErrRequest
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil || mediaType != "application/jose" && mediaType != "application/octet-stream" {
		return Object{}, ErrRequest
	}
	etag, ok := oneHeader(response.Header, "ETag")
	if !ok || !validETag(etag) || response.ContentLength <= 0 || response.ContentLength > maxResultBytes {
		return Object{}, ErrRequest
	}
	return Object{Size: response.ContentLength, ETag: etag}, nil
}

func validETag(etag string) bool {
	if len(etag) < 3 || len(etag) > 258 || etag[0] != '"' || etag[len(etag)-1] != '"' || strings.HasPrefix(etag, "W/") {
		return false
	}
	for i := 1; i < len(etag)-1; i++ {
		b := etag[i]
		if b < 0x21 || b > 0x7e || b == '"' {
			return false
		}
	}
	return true
}

func oneHeader(header http.Header, key string) (string, bool) {
	values := header.Values(key)
	if len(values) != 1 {
		return "", false
	}
	return values[0], true
}

func hasDeleteMarker(header http.Header) bool {
	values := header.Values("X-Amz-Delete-Marker")
	if len(values) > 1 {
		return true
	}
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "true")
}
