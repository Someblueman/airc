// Package fallback runs one AIRC operation over the ordered carriers a server
// offers: native IRC first, then the airc gateway's carriers, moving to the
// next only when one cannot give a definite answer.
//
// Connections are raced; operations never are. The most preferred endpoint
// is dialled first, and the next starts as soon as the previous fails or has
// gone a short stagger without progress (no connection, no bytes), so a port
// that silently drops packets costs a fraction of a second instead of a
// timeout. The first endpoint whose connection is established (native IRC
// registered; a gateway carrier connected, and for WebSocket its handshake
// done) wins, the others are closed, and the operation runs on the winner
// alone. Only when that attempt gives no definite answer does the operation
// move on to the endpoints not yet tried.
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
	"net/url"
	"os"
	"slices"
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
		return endpoint(c, ""), nil
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
	return endpoint(c, host), nil
}

// endpoint is c on host. HTTP/3 always runs over TLS, so an http3 carrier
// is one whether or not it is written +tls.
func endpoint(c protocol.Carrier, host string) Endpoint {
	if c.Name == "http3" && c.Path == "" {
		c.TLS = true
	}
	return Endpoint{Carrier: c, Host: host}
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
		out[i] = endpoint(c, "")
	}
	return out
}

// Supported reports whether this package can use a carrier name.
func Supported(name string) bool {
	switch name {
	case "irc", "irc_websocket", "irc_connect", "http", "http3", "websocket", "framed", "unix":
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
	// KeepAlive and ProbeTimeout configure native IRC connections as in
	// irc.Config, so a long-lived one notices a silent link.
	KeepAlive, ProbeTimeout time.Duration
	// ConnectTimeout bounds connecting to one gateway endpoint; zero means 3
	// seconds.
	ConnectTimeout time.Duration
	// NativeConnectTimeout bounds connecting to and registering with a
	// native IRC endpoint; zero means ConnectTimeout.
	NativeConnectTimeout time.Duration
	// Stagger is how long a connection may go without progress before the
	// next endpoint is dialled alongside it; zero means 250ms.
	Stagger time.Duration
	// CallTimeout bounds one gateway request and its answer; zero means 6
	// seconds. The caller's context bounds the whole operation.
	CallTimeout time.Duration
	// Proxy chooses the HTTP proxy for an http or websocket endpoint, given
	// its URL (scheme http or https), or nil for a direct connection. When
	// unset, HTTPS_PROXY, HTTP_PROXY and NO_PROXY apply as in
	// http.ProxyFromEnvironment, which never proxies loopback hosts. The
	// connection is tunnelled with CONNECT, so TLS stays end to end.
	Proxy func(*url.URL) (*url.URL, error)
	// Attempted, when set, learns each attempt's outcome: a connection that
	// failed, or the operation on an established one (nil for the endpoint
	// that answered). A connection closed because another won the race is
	// not an attempt. Calls are never concurrent. Errors never contain the
	// token or password.
	Attempted func(Endpoint, error)
	// Hedge makes a RoomReader's Follow follow over two gateway
	// connections at once, as FollowHedged does: the reader's own
	// connection then stays idle, and the gateway serves two follows.
	Hedge bool
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

func (c *Client) nativeConnectTimeout() time.Duration {
	if c.NativeConnectTimeout > 0 {
		return c.NativeConnectTimeout
	}
	return c.connectTimeout()
}

func (c *Client) stagger() time.Duration {
	if c.Stagger > 0 {
		return c.Stagger
	}
	return 250 * time.Millisecond
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
	if e.Name == "http3" && (e.Path != "" || !e.TLS) {
		return errors.New("http3 runs over UDP with TLS; give it a port")
	}
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

// Conn is an established connection to one endpoint: the winner of Connect.
// Its operations run on it first and, when it gives no definite answer,
// continue on the endpoints not yet tried. An operation closes the Conn;
// Close releases one that is not used.
type Conn struct {
	Endpoint Endpoint
	client   *Client
	native   *irc.Client
	gateway  gatewayConn
	// rest are the usable endpoints not yet tried; failed, the attempts so
	// far.
	rest   []Endpoint
	failed []Attempt
}

// Native is the IRC client of a native endpoint's connection, or nil. The
// caller takes it over: closing the Conn no longer closes it.
func (k *Conn) Native() *irc.Client {
	client := k.native
	k.native = nil
	return client
}

// Close closes the connection.
func (k *Conn) Close() error {
	if k.native != nil {
		return k.native.Close()
	}
	if k.gateway != nil {
		return k.gateway.Close()
	}
	return nil
}

// Connect races connections to the usable endpoints and returns the first
// established, or why none was: a *Refusal, or an *Unavailable listing every
// attempt.
func (c *Client) Connect(ctx context.Context) (*Conn, error) {
	rest, all := c.usableEndpoints()
	return c.connectNext(ctx, rest, all)
}

// usableEndpoints splits the endpoints into those that may be tried and the
// attempts that say why the others may not.
func (c *Client) usableEndpoints() ([]Endpoint, *Unavailable) {
	all := &Unavailable{}
	var rest []Endpoint
	for _, e := range c.Endpoints {
		if err := c.usable(e); err != nil {
			all.Attempts = append(all.Attempts, Attempt{Endpoint: e, Err: err})
			continue
		}
		rest = append(rest, e)
	}
	return rest, all
}

// connectNext races rest, recording failed connections in all.
func (c *Client) connectNext(ctx context.Context, rest []Endpoint, all *Unavailable) (*Conn, error) {
	if len(rest) == 0 || ctx.Err() != nil {
		return nil, all
	}
	k, left, failed, err := c.race(ctx, rest)
	all.Attempts = append(all.Attempts, failed...)
	if err != nil {
		return nil, err
	}
	if k == nil {
		return nil, all
	}
	k.client, k.rest, k.failed = c, left, slices.Clone(all.Attempts)
	return k, nil
}

// operation runs one request on an established connection.
type operation func(ctx context.Context, k *Conn) error

// run runs op on first (or, when first is nil, on the first connection
// established), then on the remaining endpoints until one answers or
// refuses.
func (c *Client) run(ctx context.Context, first *Conn, op operation) error {
	var rest []Endpoint
	all := &Unavailable{}
	if first == nil {
		rest, all = c.usableEndpoints()
	} else {
		rest, all.Attempts = first.rest, slices.Clone(first.failed)
	}
	k := first
	for {
		if k == nil {
			var err error
			if k, err = c.connectNext(ctx, rest, all); err != nil {
				return err
			}
			rest = k.rest
		}
		err := op(ctx, k)
		_ = k.Close()
		if c.Attempted != nil {
			c.Attempted(k.Endpoint, err)
		}
		if err == nil {
			return nil
		}
		if Definite(err) && !(listenerScoped(err) && len(rest) > 0) {
			var refusal *Refusal
			if !errors.As(err, &refusal) {
				err = nativeRefusal(k.Endpoint, err)
			}
			return err
		}
		all.Attempts = append(all.Attempts, Attempt{Endpoint: k.Endpoint, Err: err})
		all.MayHavePosted = all.MayHavePosted || MayHavePosted(err)
		k = nil
	}
}

// listenerScoped reports a gateway refusal that only that listener makes, such
// as its own target or operation grant, or a request shape it does not accept.
// AIRC's own refusals carry its numeric. Another carrier may answer.
func listenerScoped(err error) bool {
	var refusal *Refusal
	if !errors.As(err, &refusal) || NativeCarrier(refusal.Endpoint.Name) || refusal.UpstreamCode != 0 {
		return false
	}
	switch refusal.Code {
	case "forbidden", "invalid_request":
		return true
	}
	return false
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
	// Run is the post's run label (protocol.ValidRunID), kept in the
	// message's metadata as a native +airc/run tag is. A gateway from before
	// run labels posts without it; the Receipt's RunDropped says so.
	Run string
}

// Reaction adds Symbol to ParentID, a message in Room.
type Reaction struct {
	Room, ParentID, Symbol string
	// RequestID keys the gateway's job, so carriers retrying it add one
	// reaction. Required.
	RequestID string
}

// Receipt is the server's record of an accepted message.
type Receipt struct {
	Endpoint  Endpoint
	RequestID string
	MessageID string
	// Persisted, RecipientConnected and RecipientActive are nil when the
	// server did not say.
	Persisted          *bool
	RecipientConnected *bool
	RecipientActive    *bool
	// RunDropped reports that the message's run label did not travel: the
	// gateway predates run labels and the post went without it.
	RunDropped bool
}

// Page is a bounded read of a room's history, oldest first.
type Page struct {
	Endpoint Endpoint
	Messages []*irc.MessageMetadata
	// Status is "ok" or "more"; Cursor is opaque and only valid on the
	// carrier family that issued it.
	Status, Cursor string
	// Gap is set when a read from a message ID found it expired and began at
	// the oldest retained message instead.
	Gap bool
	// MentionsOnly marks an inbox page without direct messages; see Checked.
	MentionsOnly bool
}

// Post sends m on the first endpoint that answers. The same request ID is
// used on every attempt, so an attempt that may have landed and the next
// carrier's retry are one message.
func (c *Client) Post(ctx context.Context, m Message) (*Receipt, error) { return c.post(ctx, nil, m) }

// Post sends m on k, then on the endpoints not yet tried.
func (k *Conn) Post(ctx context.Context, m Message) (*Receipt, error) {
	return k.client.post(ctx, k, m)
}

func (c *Client) post(ctx context.Context, first *Conn, m Message) (*Receipt, error) {
	if !protocol.ValidRequestID(m.RequestID) || m.Target == "" || strings.TrimSpace(m.Body) == "" || m.ParentID != "" && !protocol.ValidMessageID(m.ParentID) {
		closeConn(first)
		return nil, errors.New("a post needs a target, a body and a valid request ID")
	}
	if m.Run != "" && !protocol.ValidRunID(m.Run) {
		closeConn(first)
		return nil, errors.New("a run label is 1-64 letters, digits, '.', '_' or '-'")
	}
	var receipt *Receipt
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			receipt, err = c.nativePost(ctx, k, m)
		} else {
			receipt, err = c.gatewayPost(ctx, k, m)
		}
		return err
	})
	return receipt, err
}

// React adds a reaction on the first endpoint that answers. Gateway carriers
// key it by request ID; AIRC itself answers a repeated reaction from the same
// author with the existing one, so a native retry adds nothing either.
func (c *Client) React(ctx context.Context, r Reaction) (*Receipt, error) {
	return c.react(ctx, nil, r)
}

// React adds a reaction on k, then on the endpoints not yet tried.
func (k *Conn) React(ctx context.Context, r Reaction) (*Receipt, error) {
	return k.client.react(ctx, k, r)
}

func (c *Client) react(ctx context.Context, first *Conn, r Reaction) (*Receipt, error) {
	if !protocol.ValidRequestID(r.RequestID) || !protocol.IsChannel(r.Room) || !protocol.ValidMessageID(r.ParentID) || !protocol.ValidReaction(r.Symbol) {
		closeConn(first)
		return nil, errors.New("a reaction needs a room, a message ID, a symbol and a valid request ID")
	}
	var receipt *Receipt
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			receipt, err = c.nativeReact(ctx, k, r)
		} else {
			receipt, err = c.gatewayReact(ctx, k, r)
		}
		return err
	})
	return receipt, err
}

// Lookup reads the receipt of an earlier post by request ID without ever
// creating a message.
func (c *Client) Lookup(ctx context.Context, requestID string) (*Receipt, error) {
	return c.lookup(ctx, nil, requestID)
}

// Lookup reads a receipt on k, then on the endpoints not yet tried.
func (k *Conn) Lookup(ctx context.Context, requestID string) (*Receipt, error) {
	return k.client.lookup(ctx, k, requestID)
}

func (c *Client) lookup(ctx context.Context, first *Conn, requestID string) (*Receipt, error) {
	if !protocol.ValidRequestID(requestID) {
		closeConn(first)
		return nil, errors.New("a valid request ID is required")
	}
	var receipt *Receipt
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			receipt, err = c.nativeLookup(ctx, k, requestID)
		} else {
			receipt, err = c.gatewayLookup(ctx, k, requestID)
		}
		return err
	})
	return receipt, err
}

// Search is a bounded search of a room's retained messages, oldest first: a
// case-insensitive substring of the body, optionally from one sender.
type Search struct {
	Room, Query, From string
	Limit             int
}

// Search reads the first page of matches.
func (c *Client) Search(ctx context.Context, s Search) (*Page, error) { return c.search(ctx, nil, s) }

// Search reads the first page of matches on k, then on the endpoints not yet
// tried.
func (k *Conn) Search(ctx context.Context, s Search) (*Page, error) {
	return k.client.search(ctx, k, s)
}

func (c *Client) search(ctx context.Context, first *Conn, s Search) (*Page, error) {
	if !protocol.IsChannel(s.Room) || s.Query == "" || !protocol.BriefText(s.Query, 256) || s.Limit < 1 || s.Limit > 1000 {
		closeConn(first)
		return nil, errors.New("search needs a room, a query of at most 256 bytes and a limit of 1-1000")
	}
	var page *Page
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			page, err = c.nativeSearch(ctx, k, s)
		} else {
			page, err = c.gatewayRead(ctx, k, request{RequestID: "search", Operation: "search", Target: targetOf(s.Room), Limit: s.Limit, Query: s.Query, From: s.From})
		}
		return err
	})
	return page, err
}

// History reads up to limit of a room's latest messages.
func (c *Client) History(ctx context.Context, room string, limit int) (*Page, error) {
	return c.history(ctx, nil, room, limit)
}

// History reads a room's latest messages on k, then on the endpoints not yet
// tried.
func (k *Conn) History(ctx context.Context, room string, limit int) (*Page, error) {
	return k.client.history(ctx, k, room, limit)
}

func (c *Client) history(ctx context.Context, first *Conn, room string, limit int) (*Page, error) {
	if !protocol.IsChannel(room) || limit < 1 || limit > 1000 {
		closeConn(first)
		return nil, errors.New("history needs a room and a limit of 1-1000")
	}
	var page *Page
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			page, err = c.nativeHistory(ctx, k, room, limit)
		} else {
			page, err = c.gatewayHistory(ctx, k, room, limit)
		}
		return err
	})
	return page, err
}

// Channels reads the server's room list: live membership, retained messages,
// last activity and topic.
func (c *Client) Channels(ctx context.Context) ([]irc.ChannelInfo, Endpoint, error) {
	return c.channels(ctx, nil)
}

// Channels reads the room list on k, then on the endpoints not yet tried.
func (k *Conn) Channels(ctx context.Context) ([]irc.ChannelInfo, Endpoint, error) {
	return k.client.channels(ctx, k)
}

func (c *Client) channels(ctx context.Context, first *Conn) ([]irc.ChannelInfo, Endpoint, error) {
	var list []irc.ChannelInfo
	var answered Endpoint
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			list, err = c.nativeChannels(ctx, k)
		} else {
			list, err = c.gatewayChannels(ctx, k)
		}
		answered = k.Endpoint
		return err
	})
	return list, answered, err
}

// Directory reads the public agent cards, or nick's alone.
func (c *Client) Directory(ctx context.Context, nick string) ([]irc.AgentCard, Endpoint, error) {
	return c.directory(ctx, nil, nick)
}

// Directory reads agent cards on k, then on the endpoints not yet tried.
func (k *Conn) Directory(ctx context.Context, nick string) ([]irc.AgentCard, Endpoint, error) {
	return k.client.directory(ctx, k, nick)
}

func (c *Client) directory(ctx context.Context, first *Conn, nick string) ([]irc.AgentCard, Endpoint, error) {
	var cards []irc.AgentCard
	var answered Endpoint
	err := c.run(ctx, first, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			cards, err = c.nativeDirectory(ctx, k, nick)
		} else {
			cards, err = c.gatewayDirectory(ctx, k, nick)
		}
		answered = k.Endpoint
		return err
	})
	return cards, answered, err
}

// Carriers asks the first endpoint that answers for the server's advertised
// carrier list (005 CARRIERS, or the gateway's capabilities); the list is
// empty when the server advertises none.
func (c *Client) Carriers(ctx context.Context) ([]protocol.Carrier, Endpoint, error) {
	var carriers []protocol.Carrier
	var answered Endpoint
	err := c.run(ctx, nil, func(ctx context.Context, k *Conn) (err error) {
		if k.native != nil {
			carriers, err = protocol.ParseCarriers(k.native.Features()["CARRIERS"])
		} else {
			carriers, err = c.gatewayCarriers(ctx, k)
		}
		answered = k.Endpoint
		return err
	})
	return carriers, answered, err
}

func closeConn(k *Conn) {
	if k != nil {
		_ = k.Close()
	}
}
