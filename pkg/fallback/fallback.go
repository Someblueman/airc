// Package fallback runs one AIRC operation over the ordered carriers a server
// offers: native IRC first, then the airc gateway's carriers, moving to the
// next only when one cannot give a definite answer.
//
// A logical operation keeps one request ID across every attempt. AIRC and the
// gateway scope request IDs to the author, never to the carrier, so a send
// that may have landed on one carrier and is retried on the next is still one
// message. A definite refusal (a rejected nickname or token, a forbidden or
// invalid request) is returned at once: another carrier reaches the same
// server and would refuse the same way.
package fallback

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Endpoint is one carrier the client may use: an advertised carrier and, for a
// TCP carrier, the host it is on when that differs from the client's Host.
type Endpoint struct {
	protocol.Carrier
	Host string
}

// String is the configured form: NAME[+tls]:[HOST:]PORT or NAME:/path.
func (e Endpoint) String() string {
	if e.Host == "" || e.Path != "" {
		return e.Carrier.String()
	}
	name := e.Name
	if e.TLS {
		name += "+tls"
	}
	return name + ":" + net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}

// ParseEndpoint accepts an advertised carrier (NAME[+tls]:PORT or
// NAME:/path) or one with its own host (NAME[+tls]:HOST:PORT, IPv6 in
// brackets).
func ParseEndpoint(s string) (Endpoint, error) {
	if c, err := protocol.ParseCarrier(s); err == nil {
		return Endpoint{Carrier: c}, nil
	}
	name, rest, ok := strings.Cut(s, ":")
	if !ok {
		return Endpoint{}, fmt.Errorf("carrier %q must be NAME:PORT, NAME:HOST:PORT or NAME:/path", s)
	}
	host, port, err := net.SplitHostPort(rest)
	if err != nil || host == "" || strings.ContainsAny(host, ",;= ") {
		return Endpoint{}, fmt.Errorf("carrier %q must be NAME:PORT, NAME:HOST:PORT or NAME:/path", s)
	}
	c, err := protocol.ParseCarrier(name + ":" + port)
	if err != nil {
		return Endpoint{}, fmt.Errorf("carrier %q: %w", s, err)
	}
	return Endpoint{Carrier: c, Host: host}, nil
}

// ParseEndpoints parses a comma-separated, ordered endpoint list.
func ParseEndpoints(list string) ([]Endpoint, error) {
	list = strings.TrimSpace(list)
	if list == "" {
		return nil, nil
	}
	parts := strings.Split(list, ",")
	if len(parts) > protocol.MaxCarriers {
		return nil, errors.New("too many carriers")
	}
	out := make([]Endpoint, 0, len(parts))
	for _, part := range parts {
		e, err := ParseEndpoint(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// FromCarriers turns an advertised list into endpoints on the client's host.
func FromCarriers(carriers []protocol.Carrier) []Endpoint {
	out := make([]Endpoint, len(carriers))
	for i, c := range carriers {
		out[i] = Endpoint{Carrier: c}
	}
	return out
}

// Supported reports whether this package can use a carrier name.
func Supported(name string) bool {
	switch name {
	case "irc", "http", "websocket", "framed", "unix":
		return true
	}
	return false
}

// Client runs operations as one author over an ordered endpoint list.
type Client struct {
	// Endpoints in preference order. Unsupported carriers are skipped.
	Endpoints []Endpoint
	// Host is where port endpoints are, unless an endpoint names its own.
	// Socket-path endpoints are used only when Host is this machine.
	Host string
	// Nick is the author. Token is its account token when the nickname is
	// registered: SASL on native IRC and the author token on the gateway.
	Nick, Token string
	// Password is the server's access token: PASS on native IRC and the
	// listener password on the gateway.
	Password string
	// TLSConfig is the trust for +tls endpoints; its ServerName defaults to
	// the endpoint's host.
	TLSConfig *tls.Config
	// ConnectTimeout bounds connecting to (and registering with) one
	// endpoint; zero means 3 seconds.
	ConnectTimeout time.Duration
	// CallTimeout bounds one gateway request and its answer; zero means 6
	// seconds. The caller's context bounds the whole operation.
	CallTimeout time.Duration
	// Attempted, when set, learns each attempt's outcome (nil for the
	// endpoint that answered). Errors never contain the token or password.
	Attempted func(Endpoint, error)
}

// Refusal is a definite answer: the server refused the request, and another
// carrier would reach the same server and refuse it the same way.
type Refusal struct {
	Endpoint Endpoint
	// Code is the gateway's error code (nick_registered,
	// authentication_failed, forbidden, invalid_request, id_conflict...) or,
	// for native IRC, the server numeric.
	Code         string
	UpstreamCode int
	Err          error
}

func (r *Refusal) Error() string {
	var gateway *gatewayError
	if r.Err != nil && !errors.As(r.Err, &gateway) {
		return fmt.Sprintf("%s refused the request: %v", r.Endpoint, r.Err)
	}
	if r.UpstreamCode != 0 {
		return fmt.Sprintf("%s refused the request: %s (AIRC %d)", r.Endpoint, r.Code, r.UpstreamCode)
	}
	return fmt.Sprintf("%s refused the request: %s", r.Endpoint, r.Code)
}

func (r *Refusal) Unwrap() error { return r.Err }

// Attempt is one endpoint that gave no definite answer.
type Attempt struct {
	Endpoint Endpoint
	Err      error
}

// Unavailable means no endpoint gave a definite answer. MayHavePosted is set
// when an attempt may have created the message (or was durably queued by the
// gateway); retrying with the same request ID resolves it without a duplicate.
type Unavailable struct {
	Attempts      []Attempt
	MayHavePosted bool
}

func (u *Unavailable) Error() string {
	if len(u.Attempts) == 0 {
		return "no usable carrier"
	}
	parts := make([]string, len(u.Attempts))
	for i, a := range u.Attempts {
		parts[i] = a.Endpoint.String() + ": " + a.Err.Error()
	}
	prefix := "no carrier answered"
	if u.MayHavePosted {
		prefix = "outcome unknown"
	}
	return prefix + " (" + strings.Join(parts, "; ") + ")"
}

// Unwrap exposes the last attempt's error.
func (u *Unavailable) Unwrap() error {
	if len(u.Attempts) == 0 {
		return nil
	}
	return u.Attempts[len(u.Attempts)-1].Err
}

// uncertain marks an attempt that may have created the message.
type uncertain struct{ err error }

func (u *uncertain) Error() string { return u.err.Error() }
func (u *uncertain) Unwrap() error { return u.err }

// MayHavePosted reports whether err leaves a send's outcome unknown.
func MayHavePosted(err error) bool {
	var u *uncertain
	var all *Unavailable
	return errors.As(err, &all) && all.MayHavePosted || errors.As(err, &u)
}

// Definite reports whether err is a server's definite answer, after which a
// client must not try another carrier.
func Definite(err error) bool {
	var refusal *Refusal
	var rejected *irc.RejectedError
	var nick *irc.NickInUseError
	return errors.As(err, &refusal) || errors.As(err, &rejected) || errors.As(err, &nick)
}

// Unreachable reports whether err is a connection-level failure or timeout:
// the server was not reached, or stopped answering before a definite answer.
func Unreachable(err error) bool {
	if err == nil || Definite(err) {
		return false
	}
	var op *net.OpError
	var dns *net.DNSError
	var syscallErr *os.SyscallError
	var all *Unavailable
	return errors.As(err, &op) || errors.As(err, &dns) || errors.As(err, &syscallErr) || errors.As(err, &all) ||
		errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) ||
		errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ENOENT)
}

func (c *Client) connectTimeout() time.Duration {
	if c.ConnectTimeout > 0 {
		return c.ConnectTimeout
	}
	return 3 * time.Second
}

func (c *Client) callTimeout() time.Duration {
	if c.CallTimeout > 0 {
		return c.CallTimeout
	}
	return 6 * time.Second
}

// local reports whether the server is on this machine, where advertised
// socket paths and plaintext ports are safe to use.
func local(host string) bool {
	if host == "" || strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// usable is why an endpoint cannot be tried, or nil.
func (c *Client) usable(e Endpoint) error {
	if !Supported(e.Name) {
		return fmt.Errorf("carrier %s is not supported by this client", e.Name)
	}
	host := c.host(e)
	if e.Path != "" && !local(c.Host) {
		return errors.New("socket path carriers are local to the server's machine")
	}
	// As on IRC, a guest may use plaintext anywhere; credentials never cross
	// a remote plaintext link, matching the native client.
	if e.Path == "" && !e.TLS && !local(host) && (c.Token != "" || c.Password != "") {
		return errors.New("remote plaintext carriers would expose the account token or server password; use a +tls carrier or an SSH tunnel")
	}
	if e.Path == "" && host == "" {
		return errors.New("no host for this carrier")
	}
	return nil
}

func (c *Client) host(e Endpoint) string {
	if e.Host != "" {
		return e.Host
	}
	return c.Host
}

// network and address are where an endpoint listens.
func (c *Client) address(e Endpoint) (string, string) {
	if e.Path != "" {
		return "unix", e.Path
	}
	return "tcp", net.JoinHostPort(c.host(e), strconv.Itoa(e.Port))
}

func (c *Client) tlsConfig(e Endpoint, protocols ...string) *tls.Config {
	if !e.TLS {
		return nil
	}
	var cfg *tls.Config
	if c.TLSConfig != nil {
		cfg = c.TLSConfig.Clone()
	} else {
		cfg = &tls.Config{}
	}
	if cfg.MinVersion < tls.VersionTLS13 {
		cfg.MinVersion = tls.VersionTLS13
	}
	if cfg.ServerName == "" {
		cfg.ServerName = c.host(e)
	}
	if len(protocols) > 0 {
		cfg.NextProtos = protocols
	}
	return cfg
}

// run tries each usable endpoint in order until one answers or refuses.
func (c *Client) run(ctx context.Context, try func(context.Context, Endpoint) error) error {
	all := &Unavailable{}
	for _, e := range c.Endpoints {
		if err := c.usable(e); err != nil {
			all.Attempts = append(all.Attempts, Attempt{Endpoint: e, Err: err})
			continue
		}
		if ctx.Err() != nil {
			break
		}
		err := try(ctx, e)
		if c.Attempted != nil {
			c.Attempted(e, err)
		}
		if err == nil {
			return nil
		}
		if Definite(err) {
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				err = nativeRefusal(e, err)
			}
			return err
		}
		all.Attempts = append(all.Attempts, Attempt{Endpoint: e, Err: err})
		all.MayHavePosted = all.MayHavePosted || MayHavePosted(err)
	}
	return all
}

func nativeRefusal(e Endpoint, err error) *Refusal {
	r := &Refusal{Endpoint: e, Code: "refused", Err: err}
	var rejected *irc.RejectedError
	if errors.As(err, &rejected) {
		r.Code = rejected.Code
		r.UpstreamCode, _ = strconv.Atoi(rejected.Code)
	}
	var nick *irc.NickInUseError
	if errors.As(err, &nick) {
		r.Code, r.UpstreamCode = "433", 433
	}
	return r
}

// Message is one post: a room or nickname target, or a reply to ParentID in
// Target's conversation.
type Message struct {
	Target, ParentID, Body string
	// RequestID makes every attempt one message. Required.
	RequestID string
}

// Receipt is the server's record of an accepted message.
type Receipt struct {
	Endpoint  Endpoint
	RequestID string
	MessageID string
	// Persisted and RecipientConnected are nil when the server did not say.
	Persisted          *bool
	RecipientConnected *bool
}

// Page is a bounded read of a room's history, oldest first.
type Page struct {
	Endpoint Endpoint
	Messages []*irc.MessageMetadata
	// Status is "ok" or "more"; Cursor is opaque and only valid on the
	// carrier family that issued it.
	Status, Cursor string
}

// Post sends m on the first endpoint that answers. The same request ID is
// used on every attempt, so an attempt that may have landed and the next
// carrier's retry are one message.
func (c *Client) Post(ctx context.Context, m Message) (*Receipt, error) {
	if !protocol.ValidRequestID(m.RequestID) || m.Target == "" || strings.TrimSpace(m.Body) == "" {
		return nil, errors.New("a post needs a target, a body and a valid request ID")
	}
	var receipt *Receipt
	err := c.run(ctx, func(ctx context.Context, e Endpoint) (err error) {
		if e.Name == "irc" {
			receipt, err = c.nativePost(ctx, e, m)
		} else {
			receipt, err = c.gatewayPost(ctx, e, m)
		}
		return err
	})
	return receipt, err
}

// Lookup reads the receipt of an earlier post by request ID without ever
// creating a message.
func (c *Client) Lookup(ctx context.Context, requestID string) (*Receipt, error) {
	if !protocol.ValidRequestID(requestID) {
		return nil, errors.New("a valid request ID is required")
	}
	var receipt *Receipt
	err := c.run(ctx, func(ctx context.Context, e Endpoint) (err error) {
		if e.Name == "irc" {
			receipt, err = c.nativeLookup(ctx, e, requestID)
		} else {
			receipt, err = c.gatewayLookup(ctx, e, requestID)
		}
		return err
	})
	return receipt, err
}

// History reads up to limit of a room's latest messages.
func (c *Client) History(ctx context.Context, room string, limit int) (*Page, error) {
	if !protocol.IsChannel(room) || limit < 1 || limit > 1000 {
		return nil, errors.New("history needs a room and a limit of 1-1000")
	}
	var page *Page
	err := c.run(ctx, func(ctx context.Context, e Endpoint) (err error) {
		if e.Name == "irc" {
			page, err = c.nativeHistory(ctx, e, room, limit)
		} else {
			page, err = c.gatewayHistory(ctx, e, room, limit)
		}
		return err
	})
	return page, err
}

// Carriers asks the first endpoint that answers for the server's advertised
// carrier list (005 CARRIERS, or the gateway's capabilities); the list is
// empty when the server advertises none.
func (c *Client) Carriers(ctx context.Context) ([]protocol.Carrier, Endpoint, error) {
	var carriers []protocol.Carrier
	var answered Endpoint
	err := c.run(ctx, func(ctx context.Context, e Endpoint) (err error) {
		if e.Name == "irc" {
			carriers, err = c.nativeCarriers(ctx, e)
		} else {
			carriers, err = c.gatewayCarriers(ctx, e)
		}
		answered = e
		return err
	})
	return carriers, answered, err
}
