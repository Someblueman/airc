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
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

type Config struct {
	Nick      string
	Username  string
	RealName  string
	Addr      string
	Network   string
	Reconnect bool
	// Ephemeral asks the server for a one-shot session: the nickname is not
	// claimed, the connection never appears in presence listings, channel
	// messages can be sent without joining, and history can be read freely.
	// Use it for short-lived commands. Servers that predate the mode ignore the
	// request; check Client.Ephemeral after Dial.
	Ephemeral    bool
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

type Client struct {
	cfg       Config
	mu        sync.Mutex
	conn      net.Conn
	nick      string
	joined    map[string]struct{}
	ephemeral bool
	features  map[string]string
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
	c := &Client{cfg: cfg, nick: cfg.Nick, joined: make(map[string]struct{}), events: make(chan Event, 256), done: make(chan struct{}), finished: make(chan struct{})}
	conn, scanner, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	go c.run(conn, scanner)
	return c, nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, *bufio.Scanner, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial airc: %w", err)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	c.setConn(conn)
	c.setEphemeral(false)
	c.setFeatures(nil)
	if c.cfg.Ephemeral {
		// Sent first so the mode applies to registration itself.
		if err := c.writeLine("EPHEMERAL\r\n"); err != nil {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, err
		}
	}
	nick := c.currentNick()
	nickLine, err := commandLine("NICK", []string{nick}, "")
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if err := c.writeLine(nickLine); err != nil {
		c.clearConn(conn)
		_ = conn.Close()
		return nil, nil, err
	}
	userLine, err := commandLine("USER", []string{c.cfg.Username, "0", "*"}, c.cfg.RealName)
	if err != nil {
		c.clearConn(conn)
		_ = conn.Close()
		return nil, nil, err
	}
	if err := c.writeLine(userLine); err != nil {
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
		if command.Name == "766" {
			c.setEphemeral(true)
		}
		if command.Name == "005" {
			c.addFeatures(command.Params)
		}
		if command.Name == "433" || command.Name == "432" {
			c.clearConn(conn)
			_ = conn.Close()
			if command.Name == "433" {
				return nil, nil, fmt.Errorf("nickname %q is already in use", nick)
			}
			return nil, nil, fmt.Errorf("nickname %q is invalid", nick)
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
	return nil, nil, fmt.Errorf("register with airc: %w", err)
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
	defer c.closeOnce.Do(func() { close(c.done) })
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
	if changed, ok := event.(*NickEvent); ok {
		c.mu.Lock()
		if strings.EqualFold(c.nick, changed.Old) {
			c.nick = changed.Nick
		}
		c.mu.Unlock()
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
		return errors.New("airc is disconnected")
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

func (c *Client) Join(channel string) error {
	line, err := commandLine("JOIN", []string{channel}, "")
	if err != nil {
		return err
	}
	c.mu.Lock()
	if _, exists := c.joined[channel]; !exists && len(c.joined) >= 64 {
		c.mu.Unlock()
		return errors.New("client is already tracking 64 channels")
	}
	c.joined[channel] = struct{}{}
	c.mu.Unlock()
	return c.writeLine(line)
}

func (c *Client) SetNick(nick string) error {
	line, err := commandLine("NICK", []string{nick}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) Part(channel, reason string) error {
	line, err := commandLine("PART", []string{channel}, reason)
	if err != nil {
		return err
	}
	if err := c.writeLine(line); err != nil {
		return err
	}
	c.mu.Lock()
	delete(c.joined, channel)
	c.mu.Unlock()
	return nil
}

// Send publishes a message. Text may span several lines (the server must
// support it; see Multiline). Line endings are normalized to LF.
func (c *Client) Send(target, message string) error { return c.sendText("PRIVMSG", target, message) }

func (c *Client) Notice(target, message string) error { return c.sendText("NOTICE", target, message) }

// NormalizeMessage returns message as Send will transmit it, with CRLF and CR
// converted to LF. Use it to compare a message you sent with what comes back.
func NormalizeMessage(message string) string { return protocol.NormalizeNewlines(message) }

func (c *Client) sendText(command, target, message string) error {
	message = NormalizeMessage(message)
	if strings.ContainsAny(target, "\r\n\x00") || strings.ContainsRune(message, 0) {
		return errors.New("IRC values may not contain NUL, and targets may not contain line breaks")
	}
	if len(message) > 4096 {
		return errors.New("message exceeds 4096 bytes")
	}
	if !utf8.ValidString(message) {
		return errors.New("message must be valid UTF-8")
	}
	if !strings.Contains(message, "\n") {
		line, err := commandLine(command, []string{target}, message)
		if err != nil {
			return err
		}
		return c.writeLine(line)
	}
	if !c.Multiline() {
		return errors.New("this server does not support multi-line messages; send a single line or upgrade aircd")
	}
	line, err := commandLine(command, []string{target}, protocol.Preview(message))
	if err != nil {
		return err
	}
	return c.writeLine("@" + protocol.BodyTag + "=" + protocol.EncodeBody(message) + " " + line)
}

func (c *Client) Who(target string) error {
	params := []string{}
	if target != "" {
		params = append(params, target)
	}
	line, err := commandLine("WHO", params, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) WhoIs(nick string) error {
	line, err := commandLine("WHOIS", []string{nick}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) Names(channel string) error {
	line, err := commandLine("NAMES", []string{channel}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

func (c *Client) History(channel string, limit int) error {
	params := []string{channel}
	if limit > 0 {
		params = append(params, fmt.Sprint(limit))
	}
	line, err := commandLine("HISTORY", params, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// HistoryAfter requests the messages for a channel or nickname that follow the
// message with ID after, oldest first. An empty after behaves like History. The
// reply ends with an EndOfHistoryEvent whose Status says whether more remain or
// the cursor has expired. target may be a nickname to read direct messages
// addressed to it. Older servers ignore the cursor, so check the status.
func (c *Client) HistoryAfter(target, after string, limit int) error {
	if after == "" {
		return c.History(target, limit)
	}
	if limit <= 0 {
		limit = 50
	}
	line, err := commandLine("HISTORY", []string{target, fmt.Sprint(limit), after}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// Observe subscribes to live messages without joining. Targets are channel
// names, or "@nick" for the direct messages addressed to a nickname. The server
// acknowledges each target with numeric 765.
func (c *Client) Observe(targets ...string) error {
	line, err := commandLine("OBSERVE", []string{strings.Join(targets, ",")}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// Topic asks for a channel's header; the reply is a TopicEvent.
func (c *Client) Topic(channel string) error {
	line, err := commandLine("TOPIC", []string{channel}, "")
	if err != nil {
		return err
	}
	return c.writeLine(line)
}

// SetTopic sets a channel's header, or clears it when text is empty. Every
// observer and member of the channel receives a TopicEvent.
func (c *Client) SetTopic(channel, text string) error {
	if strings.ContainsAny(channel, " \t\r\n\x00") || strings.ContainsAny(text, "\r\n\x00") || channel == "" {
		return errors.New("IRC values may not contain line breaks, NUL, or (for a channel) spaces")
	}
	// An explicit colon marks "set", even when the text is empty.
	return c.writeLine("TOPIC " + channel + " :" + text + "\r\n")
}

// Channels asks for every channel the server knows about, including ones with
// only retained history. The reply is ChannelEvents then an EndOfChannelsEvent.
func (c *Client) Channels() error { return c.writeLine("CHANNELS\r\n") }

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

func commandLine(name string, params []string, trailing string) (string, error) {
	for _, param := range params {
		if param == "" || strings.HasPrefix(param, ":") || strings.ContainsAny(param, " \t\r\n\x00") {
			return "", errors.New("IRC parameters must be non-empty single-line tokens")
		}
	}
	if strings.ContainsAny(trailing, "\r\n\x00") {
		return "", errors.New("IRC trailing text may not contain line breaks or NUL")
	}
	line := protocol.Format("", name, params, trailing)
	if !strings.HasSuffix(line, "\r\n") {
		return "", errors.New("IRC command exceeds maximum line length")
	}
	return line, nil
}
