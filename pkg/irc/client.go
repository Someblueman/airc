package irc

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

type Config struct {
	Nick         string
	Username     string
	RealName     string
	Addr         string
	Network      string
	Reconnect    bool
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Client struct {
	cfg       Config
	mu        sync.Mutex
	conn      net.Conn
	joined    map[string]struct{}
	events    chan Event
	done      chan struct{}
	finished  chan struct{}
	closeOnce sync.Once
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
	if cfg.Network == "" {
		cfg.Network = "tcp"
	}
	if cfg.Addr == "" {
		if cfg.Network == "unix" {
			return nil, errors.New("unix socket address is required")
		}
		cfg.Addr = "127.0.0.1:6667"
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
	c := &Client{cfg: cfg, joined: make(map[string]struct{}), events: make(chan Event, 256), done: make(chan struct{}), finished: make(chan struct{})}
	backoff := cfg.MinBackoff
	var conn net.Conn
	var scanner *bufio.Scanner
	for {
		var err error
		conn, scanner, err = c.connect(ctx)
		if err == nil {
			break
		}
		if !cfg.Reconnect {
			return nil, err
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		backoff = growBackoff(backoff, cfg.MaxBackoff)
	}
	go c.run(conn, scanner)
	return c, nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, *bufio.Scanner, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial agent-irc: %w", err)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	c.setConn(conn)
	if err := c.writeLine(protocol.Format("", "NICK", []string{c.cfg.Nick}, "")); err != nil {
		c.clearConn(conn)
		_ = conn.Close()
		return nil, nil, err
	}
	if err := c.writeLine(protocol.Format("", "USER", []string{c.cfg.Username, "0", "*"}, c.cfg.RealName)); err != nil {
		c.clearConn(conn)
		_ = conn.Close()
		return nil, nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	} else {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
	}
	scanner := newScanner(conn)
	for scanner.Scan() {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		command, err := c.dispatch(scanner.Text())
		if err != nil {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, err
		}
		if command.Name == "433" {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, fmt.Errorf("nickname %q is already in use", c.cfg.Nick)
		}
		if command.Name == "001" {
			if err := c.rejoin(); err != nil {
				c.clearConn(conn)
				_ = conn.Close()
				return nil, nil, err
			}
			_ = conn.SetReadDeadline(time.Time{})
			return conn, scanner, nil
		}
	}
	err = scanner.Err()
	if err == nil {
		err = errors.New("server closed connection before registration")
	}
	c.clearConn(conn)
	_ = conn.Close()
	return nil, nil, fmt.Errorf("register with agent-irc: %w", err)
}

func newScanner(conn net.Conn) *bufio.Scanner {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1024), protocol.MaxLineLength+2)
	return scanner
}

func (c *Client) run(conn net.Conn, scanner *bufio.Scanner) {
	defer close(c.finished)
	defer close(c.events)
	defer c.clearConn(conn)
	backoff := c.cfg.MinBackoff
	for {
		err := c.readConnection(conn, scanner)
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
		newConn, newScanner, connectErr := c.connect(context.Background())
		if connectErr != nil {
			c.publish(&ConnectionEvent{Type: "connection", Connected: false, Error: connectErr.Error()})
			backoff = growBackoff(backoff, c.cfg.MaxBackoff)
			continue
		}
		conn, scanner, backoff = newConn, newScanner, c.cfg.MinBackoff
		c.publish(&ConnectionEvent{Type: "connection", Connected: true})
	}
}

func (c *Client) readConnection(conn net.Conn, scanner *bufio.Scanner) error {
	for scanner.Scan() {
		_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		if _, err := c.dispatch(scanner.Text()); err != nil {
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

func (c *Client) dispatch(line string) (*protocol.Command, error) {
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
	c.publish(event)
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
		if err := c.writeLine(protocol.Format("", "JOIN", []string{channel}, "")); err != nil {
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

func (c *Client) setConn(conn net.Conn) { c.mu.Lock(); c.conn = conn; c.mu.Unlock() }

func (c *Client) clearConn(conn net.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
}

func (c *Client) writeLine(line string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errors.New("agent-irc is disconnected")
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
	data := []byte(line)
	for len(data) > 0 {
		n, err := c.conn.Write(data)
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

func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}

func (c *Client) Join(channel string) error {
	c.mu.Lock()
	c.joined[channel] = struct{}{}
	c.mu.Unlock()
	return c.writeLine(protocol.Format("", "JOIN", []string{channel}, ""))
}

func (c *Client) Part(channel, reason string) error {
	if err := c.writeLine(protocol.Format("", "PART", []string{channel}, reason)); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.joined, channel)
	c.mu.Unlock()
	return nil
}

func (c *Client) Send(target, message string) error {
	if strings.ContainsAny(target+message, "\r\n\x00") {
		return errors.New("IRC values may not contain line breaks or NUL")
	}
	if len(message) > 4096 {
		return errors.New("message exceeds 4096 bytes")
	}
	return c.writeLine(protocol.Format("", "PRIVMSG", []string{target}, message))
}

func (c *Client) Notice(target, message string) error {
	if strings.ContainsAny(target+message, "\r\n\x00") {
		return errors.New("IRC values may not contain line breaks or NUL")
	}
	return c.writeLine(protocol.Format("", "NOTICE", []string{target}, message))
}

func (c *Client) Who(target string) error {
	return c.writeLine(protocol.Format("", "WHO", []string{target}, ""))
}

func (c *Client) WhoIs(nick string) error {
	return c.writeLine(protocol.Format("", "WHOIS", []string{nick}, ""))
}

func (c *Client) Names(channel string) error {
	return c.writeLine(protocol.Format("", "NAMES", []string{channel}, ""))
}

func (c *Client) History(channel string, limit int) error {
	params := []string{channel}
	if limit > 0 {
		params = append(params, fmt.Sprint(limit))
	}
	return c.writeLine(protocol.Format("", "HISTORY", params, ""))
}

// Raw sends one parsed IRC-style command, which is useful for less common extensions.
func (c *Client) Raw(line string) error {
	if strings.ContainsAny(line, "\r\n\x00") {
		return errors.New("raw command must contain exactly one line")
	}
	if _, err := protocol.Parse(line); err != nil {
		return err
	}
	return c.writeLine(strings.TrimSpace(line) + "\r\n")
}

func (c *Client) Close() error {
	var closeErr error
	c.closeOnce.Do(func() {
		if c.Connected() {
			closeErr = c.writeLine(protocol.Format("", "QUIT", nil, "Client closed"))
		}
		close(c.done)
		c.mu.Lock()
		if c.conn != nil {
			_ = c.conn.Close()
		}
		c.mu.Unlock()
	})
	<-c.finished
	return closeErr
}

func growBackoff(current, maximum time.Duration) time.Duration {
	next := current * 2
	if next > maximum {
		return maximum
	}
	return next
}
