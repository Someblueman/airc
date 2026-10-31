package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"sync/atomic"
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
	IdentityToken string // saved account credential; SASL PLAIN login unless CreateAccount; never sent as chat
	CreateAccount bool   // idempotent registration with the same credential
	// GatewayToken and Source are for a gateway connecting on behalf of
	// another client. The connection first asserts that client's real source,
	// an IP address or a label such as "listener:mqtt" for carriers without
	// addresses, so the server applies its per-address limits to the client
	// rather than to the gateway. Set both or neither; the server must
	// advertise WEBIRC.
	GatewayToken string
	Source       string
	// DialContext, when set, opens the TCP or Unix connection in place of a
	// net.Dialer; TLS, when configured, is layered on the connection it
	// returns. It lets a caller observe or route the connection.
	DialContext  func(ctx context.Context, network, address string) (net.Conn, error)
	MinBackoff   time.Duration
	MaxBackoff   time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	// KeepAlive, when set, probes a connection that has read nothing for
	// that long (see Probe), allowing ProbeTimeout (default 5s) for the
	// answer. A link that went silent is then dropped in KeepAlive plus
	// ProbeTimeout instead of ReadTimeout.
	KeepAlive    time.Duration
	ProbeTimeout time.Duration
}

// ErrStalled is the connection error after a probe went unanswered.
var ErrStalled = errors.New("server stopped answering")

// ErrDaemonTooOld is what a dial ends with when the daemon advertised no AIRC
// level, or one below protocol.Level: this client needs every feature of its
// own level, so it refuses the daemon and closes the connection instead of
// running without them, and a Reconnect client stops reconnecting. The error
// is a *DaemonTooOldError, which carries both levels. A daemon at a higher
// level is fine. aircd, airc and airc-gateway are replaced together, so the
// cure is to upgrade aircd and restart it.
var ErrDaemonTooOld = errors.New("aircd is older than this client")

// DaemonTooOldError is ErrDaemonTooOld with the daemon's AIRC level (0 when it
// advertised none) and the level this client needs.
type DaemonTooOldError struct {
	Level int
	Need  int
}

func (e *DaemonTooOldError) Error() string {
	if e.Level == 0 {
		return "aircd advertises no AIRC level: upgrade aircd and restart it"
	}
	return fmt.Sprintf("aircd advertises AIRC level %d; this client needs %d: upgrade aircd and restart it", e.Level, e.Need)
}

func (e *DaemonTooOldError) Is(target error) bool { return target == ErrDaemonTooOld }

// AnswerWait and AnswerProbeTimeout suit ProbeAfter for a command whose answer
// a live server sends at once: a silent server is detected in 1.3s.
const (
	AnswerWait         = 300 * time.Millisecond
	AnswerProbeTimeout = time.Second
)

type Client struct {
	cfg        Config
	mu         sync.Mutex
	writeMu    sync.Mutex
	conn       net.Conn
	nick       string
	joined     map[string]struct{}
	away       string
	monitoring []string
	ephemeral  bool
	accountID  string
	// resume is the secret the server issued at registration (767). On
	// reconnect it is presented with RESUME, so a session the server still
	// holds for a link that went silent is taken over instead of refusing
	// the nickname.
	resume      string
	features    map[string]string
	channelsRaw bool
	agentsRaw   bool
	signalsRaw  bool
	events      chan Event
	done        chan struct{}
	finished    chan struct{}
	closeOnce   sync.Once
	stop        context.CancelFunc
	// probe is the outstanding liveness probe's token, on probeConn;
	// stalled is the connection a probe found silent.
	probe     string
	probeConn net.Conn
	probeSeq  uint64
	stalled   net.Conn
	lastRead  atomic.Int64
	probing   atomic.Bool
	ended     error // why the connection ended, once finished is closed
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
	if (cfg.GatewayToken != "" || cfg.Source != "") && (!validIdentityToken(cfg.GatewayToken) || !protocol.ValidSource(cfg.Source)) {
		return nil, errors.New("a gateway connection needs a valid gateway credential and source")
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
	if cfg.ProbeTimeout <= 0 {
		cfg.ProbeTimeout = 5 * time.Second
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
			c.mu.Lock()
			c.ended = err
			c.mu.Unlock()
			return
		}
		if err != nil {
			c.publish(&ConnectionEvent{Type: "connection", Connected: false, Error: err.Error()})
		}
		for {
			timer := time.NewTimer(backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)+1)))
			select {
			case <-c.done:
				timer.Stop()
				return
			case <-timer.C:
			}
			newConn, newScanner, connectErr := c.connect(ctx)
			if connectErr != nil {
				c.publish(&ConnectionEvent{Type: "connection", Connected: false, Error: connectErr.Error()})
				if errors.Is(connectErr, ErrDaemonTooOld) {
					// No reconnect cures a daemon below this client's
					// level: the connection ends as it would without
					// Reconnect, and Err says why.
					c.mu.Lock()
					c.ended = connectErr
					c.mu.Unlock()
					return
				}
				backoff = growBackoff(backoff, c.cfg.MaxBackoff)
				continue
			}
			conn, scanner, backoff = newConn, newScanner, c.cfg.MinBackoff
			c.publish(&ConnectionEvent{Type: "connection", Connected: true})
			break
		}
	}
}

func (c *Client) readConnection(ctx context.Context, conn net.Conn, scanner *bufio.Scanner) error {
	c.lastRead.Store(time.Now().UnixNano())
	if c.cfg.KeepAlive > 0 {
		stop := make(chan struct{})
		defer close(stop)
		go c.keepAlive(conn, stop)
	}
	for scanner.Scan() {
		c.lastRead.Store(time.Now().UnixNano())
		if c.probing.Load() {
			c.settleProbe(conn) // any line proves the server is still answering
		}
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
	c.mu.Lock()
	stalled := c.stalled == conn
	c.mu.Unlock()
	if stalled {
		return ErrStalled
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return errors.New("server closed connection")
}

// keepAlive probes conn whenever it has read nothing for KeepAlive.
func (c *Client) keepAlive(conn net.Conn, stop <-chan struct{}) {
	ticker := time.NewTicker(max(c.cfg.KeepAlive/4, 10*time.Millisecond))
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if time.Since(time.Unix(0, c.lastRead.Load())) >= c.cfg.KeepAlive && !c.backlogged() {
			_ = c.probeOn(conn, c.cfg.ProbeTimeout)
		}
	}
}

// Probe checks that the server is still answering: it sends a PING and, if
// nothing at all (its PONG or any other line) has arrived within timeout,
// drops the connection as stalled (ErrStalled), so a silent link fails in
// about timeout rather than ReadTimeout. Without Reconnect, Events and Done
// then close. A probe already outstanding is not repeated.
func (c *Client) Probe(timeout time.Duration) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("airc is disconnected: %w", net.ErrClosed)
	}
	return c.probeOn(conn, timeout)
}

func (c *Client) probeOn(conn net.Conn, timeout time.Duration) error {
	c.mu.Lock()
	if c.conn != conn || c.probeConn == conn {
		c.mu.Unlock()
		return nil
	}
	c.probeSeq++
	token := fmt.Sprintf("%s%d", probePrefix, c.probeSeq)
	c.probe, c.probeConn = token, conn
	c.probing.Store(true)
	c.mu.Unlock()
	time.AfterFunc(timeout, func() {
		c.mu.Lock()
		// A full event queue stops the reader, not the server: the answer
		// waits unread behind events the caller has not taken yet.
		silent := c.probeConn == conn && c.probe == token && !c.backlogged()
		if !silent && c.probeConn == conn && c.probe == token {
			c.probe, c.probeConn = "", nil
			c.probing.Store(false)
		}
		if silent {
			c.probe, c.probeConn, c.stalled = "", nil, conn
			c.probing.Store(false)
		}
		c.mu.Unlock()
		if silent {
			_ = conn.Close()
		}
	})
	return c.writeLine(protocol.Format("", "PING", nil, token))
}

// ProbeAfter probes the connection (see Probe) if the returned stop has not
// been called within wait: for a caller expecting an answer, a server that
// stays silent past wait then fails in wait plus timeout.
func (c *Client) ProbeAfter(wait, timeout time.Duration) (stop func() bool) {
	return time.AfterFunc(wait, func() { _ = c.Probe(timeout) }).Stop
}

// backlogged reports a full event queue, which blocks the reader.
func (c *Client) backlogged() bool { return len(c.events) == cap(c.events) }

// probePrefix marks the client's own probe tokens, whose PONGs are not events.
const probePrefix = "airc-probe-"

// settleProbe clears conn's outstanding probe: the server answered.
func (c *Client) settleProbe(conn net.Conn) {
	c.mu.Lock()
	if c.probeConn == conn {
		c.probe, c.probeConn = "", nil
		c.probing.Store(false)
	}
	c.mu.Unlock()
}

// ownProbe reports a PONG answering one of the client's probes.
func ownProbe(command *protocol.Command) bool {
	token := command.Trailing
	if token == "" {
		token, _ = command.Param(len(command.Params) - 1)
	}
	return strings.HasPrefix(token, probePrefix)
}

func (c *Client) dispatch(ctx context.Context, line string) (*protocol.Command, error) {
	event, command, err := decodeEvent(line)
	if err != nil {
		return nil, err
	}
	if command.Name == "768" || command.Name == "769" {
		c.mu.Lock()
		channelsRaw := c.channelsRaw
		c.mu.Unlock()
		if channelsRaw {
			event = &RawEvent{Type: "raw", Command: command.Name, Prefix: command.Prefix, Params: append([]string(nil), command.Params...), Trailing: command.Trailing, Tags: maps.Clone(command.Tags)}
		}
	}
	if command.Name == "763" || command.Name == "764" {
		c.mu.Lock()
		agentsRaw := c.agentsRaw
		c.mu.Unlock()
		if agentsRaw {
			event = &RawEvent{Type: "raw", Command: command.Name, Prefix: command.Prefix, Params: append([]string(nil), command.Params...), Trailing: command.Trailing, Tags: maps.Clone(command.Tags)}
		}
	}
	if command.Name == "780" {
		c.mu.Lock()
		signalsRaw := c.signalsRaw
		c.mu.Unlock()
		if signalsRaw {
			event = &RawEvent{Type: "raw", Command: command.Name, Prefix: command.Prefix, Params: append([]string(nil), command.Params...), Trailing: command.Trailing, Tags: maps.Clone(command.Tags)}
		}
	}
	if command.Name == "779" && len(command.Params) > 1 && protocol.ValidMessageID(command.Params[1]) {
		// AUTH, REGISTER and SASL logins name the account's stable ID.
		c.mu.Lock()
		c.accountID = command.Params[1]
		c.mu.Unlock()
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
	if command.Name == "PONG" && ownProbe(command) {
		return command, nil // the client's own probe, not an event
	}
	if changed, ok := event.(*NickEvent); ok {
		c.mu.Lock()
		if strings.EqualFold(c.nick, changed.Old) {
			c.nick = changed.Nick
		}
		c.mu.Unlock()
	}
	if kick, ok := event.(*KickEvent); ok {
		c.mu.Lock()
		if strings.EqualFold(c.nick, kick.Agent) {
			delete(c.joined, kick.Channel)
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
	away, monitoring := c.away, append([]string(nil), c.monitoring...)
	c.mu.Unlock()
	if away != "" {
		line, err := commandLine("AWAY", nil, away)
		if err != nil {
			return err
		}
		if err := c.writeLine(line); err != nil {
			return err
		}
	}
	if len(monitoring) > 0 {
		if err := c.restoreMonitor(monitoring); err != nil {
			return err
		}
	}
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

// Supports reports whether the server has a feature: one its AIRC level
// grants (protocol.Features; every feature of this client's own level is
// guaranteed, since Dial refuses a daemon below it), or one it advertised as
// a token of its own, which is how a daemon says what depends on its
// configuration (ADMIN, WAKE, IDEMPOTENCY, ...). Features are known once
// Dial returns.
func (c *Client) Supports(feature string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.Supports(c.features, feature)
}

// Level is the AIRC level the server advertised (AIRC=N), at least
// protocol.Level once Dial has returned; 0 before 005 has been read.
func (c *Client) Level() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return protocol.LevelOf(c.features)
}

// Features returns a snapshot of the server's advertised tokens: the AIRC
// level, the tokens with a value (HISTORY, PASTE_BYTES, MONITOR, CARRIERS,
// ...) and those that depend on the daemon's configuration. Ask Supports
// about a feature; read a token's value here.
func (c *Client) Features() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	features := make(map[string]string, len(c.features))
	maps.Copy(features, c.features)
	return features
}

// Multiline reports whether the server accepts and delivers messages that
// contain line breaks.
func (c *Client) Multiline() bool { return c.Supports("MULTILINE") }

func (c *Client) setEphemeral(value bool) { c.mu.Lock(); c.ephemeral = value; c.mu.Unlock() }

func (c *Client) setAccountID(id string) { c.mu.Lock(); c.accountID = id; c.mu.Unlock() }

func (c *Client) setResume(secret string) { c.mu.Lock(); c.resume = secret; c.mu.Unlock() }

// Resumable reports whether the server issued this connection a resume
// secret, so a reconnect can take the session over if the server still
// holds it (RESUME=1).
func (c *Client) Resumable() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.resume != ""
}

// Ephemeral reports whether the server granted a one-shot session.
func (c *Client) Ephemeral() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ephemeral
}

// AccountID is the stable ID of the account this session authenticated as
// (from the server's 779 reply to AUTH, REGISTER or a SASL login), or "" for a
// guest or a server that does not report one.
func (c *Client) AccountID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accountID
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
		return fmt.Errorf("airc is disconnected: %w", net.ErrClosed)
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

// Err reports why a client without Reconnect lost its connection, such as
// ErrStalled, once Events has closed; nil while connected or after Close.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

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
