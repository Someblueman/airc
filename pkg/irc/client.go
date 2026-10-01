package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type Config struct {
	TLSConfig   *tls.Config
	AccessToken string // connection credential, separate from account and operator credentials
	Nick        string
	Username    string
	RealName    string
	Addr        string
	Network     string
	Reconnect   bool
	// Ephemeral asks the server for a one-shot session: the nickname is not
	// claimed, the connection never appears in presence listings, channel
	// messages can be sent without joining, and history can be read freely.
	// Use it for short-lived commands. Servers that predate the mode ignore the
	// request; check Client.Ephemeral after Dial.
	Ephemeral     bool
	IdentityToken string // random account credential; never printed or sent as chat
	CreateAccount bool   // idempotent registration with the same credential
	MinBackoff    time.Duration
	MaxBackoff    time.Duration
	ReadTimeout   time.Duration
	WriteTimeout  time.Duration
}

type Client struct {
	cfg       Config
	mu        sync.Mutex
	writeMu   sync.Mutex
	conn      net.Conn
	nick      string
	joined    map[string]struct{}
	ephemeral bool
	features  map[string]string
	events    chan Event
	done      chan struct{}
	finished  chan struct{}
	closeOnce sync.Once
	stop      context.CancelFunc
}

func Dial(cfg Config) (*Client, error) { return DialContext(context.Background(), cfg) }

func DialContext(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Nick == "" {
		return nil, errors.New("nickname is required")
	}
	if cfg.Username == "" {
		cfg.Username = cfg.Nick
	}
	if cfg.RealName == "" {
		cfg.RealName = cfg.Nick
	}
	if strings.ContainsAny(cfg.Nick+cfg.Username+cfg.RealName, "\r\n\x00") || strings.ContainsAny(cfg.Nick+cfg.Username, " \t") || strings.HasPrefix(cfg.Nick, ":") || strings.HasPrefix(cfg.Username, ":") {
		return nil, errors.New("nickname and username must be single-line values; real name must not contain control characters")
	}
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	if cfg.Addr == "" {
		if cfg.Network == "unix" {
			return nil, errors.New("unix socket address is required")
		}
		cfg.Addr = "127.0.0.1:6667"
	}
	if cfg.TLSConfig != nil {
		if cfg.Network != "tcp" && cfg.Network != "tcp4" && cfg.Network != "tcp6" {
			return nil, errors.New("TLS requires TCP")
		}
		cfg.TLSConfig = cfg.TLSConfig.Clone()
		cfg.TLSConfig.MinVersion = tls.VersionTLS13
	}
	if cfg.AccessToken != "" && !validIdentityToken(cfg.AccessToken) {
		return nil, errors.New("invalid connection credential")
	}
	if cfg.MinBackoff <= 0 {
		cfg.MinBackoff = 250 * time.Millisecond
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = 10 * time.Second
	}
	if cfg.MaxBackoff < cfg.MinBackoff {
		cfg.MaxBackoff = cfg.MinBackoff
	}
	if cfg.ReadTimeout <= 0 {
		cfg.ReadTimeout = 2 * time.Minute
	}
	if cfg.WriteTimeout <= 0 {
		cfg.WriteTimeout = 10 * time.Second
	}
	lifetime, stop := context.WithCancel(context.Background())
	c := &Client{cfg: cfg, nick: cfg.Nick, joined: make(map[string]struct{}), events: make(chan Event, 256), done: make(chan struct{}), finished: make(chan struct{}), stop: stop}
	conn, scanner, err := c.connect(ctx)
	if err != nil {
		stop()
		return nil, err
	}
	go c.run(lifetime, conn, scanner)
	return c, nil
}

func (c *Client) run(ctx context.Context, conn net.Conn, scanner *bufio.Scanner) {
	defer c.stop()
	defer close(c.finished)
	defer close(c.events)
	defer c.clearConn(conn)
	defer c.closeOnce.Do(func() { close(c.done) })
	backoff := c.cfg.MinBackoff
	for {
		err := c.readConnection(ctx, conn, scanner)
		c.clearConn(conn)
		_ = conn.Close()
		select {
		case <-c.done:
			return
		default:
		}
		if !c.cfg.Reconnect {
			return
		}
		c.publish(&ConnectionEvent{Type: "connection", Connected: false, Error: err.Error()})
		timer := time.NewTimer(backoff)
		select {
		case <-c.done:
			timer.Stop()
			return
		case <-timer.C:
		}
		newConn, newScanner, connectErr := c.connect(ctx)
		if connectErr != nil {
			c.publish(&ConnectionEvent{Type: "connection", Connected: false, Error: connectErr.Error()})
			backoff = growBackoff(backoff, c.cfg.MaxBackoff)
			continue
		}
		conn, scanner, backoff = newConn, newScanner, c.cfg.MinBackoff
		c.publish(&ConnectionEvent{Type: "connection", Connected: true})
	}
}

func (c *Client) readConnection(ctx context.Context, conn net.Conn, scanner *bufio.Scanner) error {
	for scanner.Scan() {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		if _, err := c.dispatch(ctx, scanner.Text()); err != nil {
			return err
		}
		select {
		case <-c.done:
			return nil
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("server closed connection")
}

func (c *Client) dispatch(ctx context.Context, line string) (*protocol.Command, error) {
	event, command, err := decodeEvent(line)
	if err != nil {
		return nil, err
	}
	if command.Name == "PING" {
		token := command.Trailing
		if token == "" {
			token, _ = command.Param(0)
		}
		if err := c.writeLine(protocol.Format("", "PONG", nil, token)); err != nil {
			return nil, err
		}
	}
	if changed, ok := event.(*NickEvent); ok {
		c.mu.Lock()
		if strings.EqualFold(c.nick, changed.Old) {
			c.nick = changed.Nick
		}
		c.mu.Unlock()
	}
	select {
	case c.events <- event:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, net.ErrClosed
	}
	return command, nil
}

func (c *Client) rejoin() error {
	c.mu.Lock()
	channels := make([]string, 0, len(c.joined))
	for channel := range c.joined {
		channels = append(channels, channel)
	}
	c.mu.Unlock()
	for _, channel := range channels {
		line, err := commandLine("JOIN", []string{channel}, "")
		if err != nil {
			return err
		}
		if err := c.writeLine(line); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) publish(event Event) {
	select {
	case <-c.done:
	case c.events <- event:
	}
}

func (c *Client) setFeatures(features map[string]string) {
	c.mu.Lock()
	c.features = features
	c.mu.Unlock()
}

// addFeatures records the KEY or KEY=VALUE tokens of an ISUPPORT (005) reply.
func (c *Client) addFeatures(params []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.features == nil {
		c.features = make(map[string]string)
	}
	for _, token := range params[min(1, len(params)):] {
		key, value, _ := strings.Cut(token, "=")
		c.features[strings.ToUpper(key)] = value
	}
}

// Supports reports whether the server advertised a feature, such as "MULTILINE"
// or "MENTIONS". Features are known once Dial returns.
func (c *Client) Supports(feature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.features[strings.ToUpper(feature)]
	return ok && value != "0"
}

// Features returns a snapshot of the server's advertised capabilities.
func (c *Client) Features() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	features := make(map[string]string, len(c.features))
	for key, value := range c.features {
		features[key] = value
	}
	return features
}

// Multiline reports whether the server accepts and delivers messages that
// contain line breaks.
func (c *Client) Multiline() bool { return c.Supports("MULTILINE") }

func (c *Client) setEphemeral(value bool) { c.mu.Lock(); c.ephemeral = value; c.mu.Unlock() }

// Ephemeral reports whether the server granted a one-shot session.
func (c *Client) Ephemeral() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ephemeral
}

func (c *Client) setConn(conn net.Conn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return false
	default:
		c.conn = conn
		return true
	}
}

func (c *Client) clearConn(conn net.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) writeLine(line string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return errors.New("airc is disconnected")
	}
	select {
	case <-c.done:
		return net.ErrClosed
	default:
	}
	_ = conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
	data := []byte(line)
	for len(data) > 0 {
		n, err := conn.Write(data)
		if err != nil {
			return fmt.Errorf("write IRC command: %w", err)
		}
		if n == 0 {
			return errors.New("write IRC command: zero bytes written")
		}
		data = data[n:]
	}
	return nil
}

func (c *Client) Events() <-chan Event  { return c.events }
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) Nick() string { return c.currentNick() }

func (c *Client) currentNick() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.nick
}

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.done)
		c.stop()
		c.mu.Lock()
		conn := c.conn
		c.conn = nil
		c.mu.Unlock()
		if conn != nil {
			if c.writeMu.TryLock() {
				_ = conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
				_, _ = conn.Write([]byte(protocol.Format("", "QUIT", nil, "Client closed")))
				c.writeMu.Unlock()
			}
			// Closing the socket interrupts reads and writes, including a writer
			// holding writeMu. A graceful QUIT must never delay resource cleanup.
			_ = conn.Close()
		}
	})
	<-c.finished
	return nil
}

func growBackoff(current, maximum time.Duration) time.Duration {
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}
