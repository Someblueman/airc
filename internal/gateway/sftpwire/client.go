package sftpwire

import (
	"context"
	"errors"
	"io"
	"net"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/ssh"
)

var (
	ErrInvalid = errors.New("invalid bounded SFTP request or response")
	ErrClosed  = errors.New("SFTP client is closed")
)

const maxPathBytes = 4096

// Client owns one pinned-key SSH connection and one SFTP v3 session. Calls are
// serial; Close interrupts active I/O and joins its protocol workers.
type Client struct {
	ctx          context.Context
	raw          net.Conn
	conn         ssh.Conn
	channel      ssh.Channel
	globalDone   chan struct{}
	incomingDone chan struct{}
	requestsDone chan struct{}
	stderrDone   chan struct{}
	stopCancel   func() bool
	cancelDone   chan struct{}
	opMu         sync.Mutex
	closed       atomic.Bool
	closeOnce    sync.Once
	requestID    uint32
}

// Dial uses public-key authentication only, pins the exact host key, and opens
// only the SFTP subsystem on a single SSH session channel.
func Dial(ctx context.Context, address, user string, signer ssh.Signer, hostKey ssh.PublicKey) (*Client, error) {
	if ctx == nil || ctx.Err() != nil || !numericAddress(address) || !validUser(user) || signer == nil || signer.PublicKey() == nil || hostKey == nil {
		return nil, ErrInvalid
	}
	deadline, ok := ctx.Deadline()
	if !ok || !time.Now().Before(deadline) || time.Until(deadline) > 15*time.Second {
		return nil, ErrInvalid
	}
	pinned, err := ssh.ParsePublicKey(hostKey.Marshal())
	if err != nil {
		return nil, ErrInvalid
	}
	remote, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	if err := remote.SetDeadline(deadline); err != nil {
		_ = remote.Close()
		return nil, err
	}
	cancelDone := make(chan struct{})
	stopCancel := context.AfterFunc(ctx, func() {
		_ = remote.Close()
		close(cancelDone)
	})
	conf := &ssh.ClientConfig{
		User:              user,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   ssh.FixedHostKey(pinned),
		HostKeyAlgorithms: ssh.SupportedAlgorithms().HostKeys,
	}
	conn, chans, requests, err := ssh.NewClientConn(remote, address, conf)
	if err != nil {
		_ = remote.Close()
		joinCancel(stopCancel, cancelDone)
		return nil, err
	}
	s := &Client{ctx: ctx, raw: remote, conn: conn, globalDone: make(chan struct{}), incomingDone: make(chan struct{}), stopCancel: stopCancel, cancelDone: cancelDone}
	go s.drainGlobalRequests(requests)
	go s.rejectIncomingChannels(chans)
	if ctx.Err() != nil {
		_ = s.Close()
		return nil, ctx.Err()
	}
	channel, channelRequests, err := conn.OpenChannel("session", nil)
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	s.channel = channel
	s.requestsDone = make(chan struct{})
	s.stderrDone = make(chan struct{})
	go s.drainChannelRequests(channelRequests)
	go s.drainStderr(channel.Stderr())
	var subsystem encoder
	subsystem.text("sftp")
	ok, err = channel.SendRequest("subsystem", true, subsystem)
	if err != nil || !ok {
		_ = s.Close()
		return nil, ErrInvalid
	}
	if err := s.initSFTP(); err != nil {
		_ = s.Close()
		return nil, err
	}
	if ctx.Err() != nil {
		_ = s.Close()
		return nil, ctx.Err()
	}
	return s, nil
}

func (c *Client) drainChannelRequests(requests <-chan *ssh.Request) {
	defer close(c.requestsDone)
	for request := range requests {
		if request.WantReply {
			_ = request.Reply(false, nil)
		}
	}
}

func (c *Client) drainGlobalRequests(requests <-chan *ssh.Request) {
	defer close(c.globalDone)
	for request := range requests {
		if request.WantReply {
			_ = request.Reply(false, nil)
		}
	}
}

func (c *Client) rejectIncomingChannels(channels <-chan ssh.NewChannel) {
	defer close(c.incomingDone)
	for channel := range channels {
		_ = channel.Reject(ssh.Prohibited, "inbound channels are disabled")
	}
}

func (c *Client) drainStderr(stderr io.Reader) {
	defer close(c.stderrDone)
	if n, _ := io.CopyN(io.Discard, stderr, 8193); n > 8192 {
		_ = c.raw.Close()
	}
}

func (c *Client) initSFTP() error {
	var version encoder
	version.u32(3)
	if err := writePacket(c.channel, packetInit, version); err != nil {
		return err
	}
	typ, payload, err := readPacket(c.channel)
	if err != nil || typ != packetVersion {
		return ErrInvalid
	}
	d := decoder{data: payload}
	versionNumber, err := d.u32()
	if err != nil || versionNumber != 3 {
		return ErrInvalid
	}
	seen := make(map[string]bool)
	fsync := false
	for len(d.data) != 0 {
		if len(seen) >= 64 {
			return ErrInvalid
		}
		name, err := d.string(4096)
		if err != nil || len(name) == 0 {
			return ErrInvalid
		}
		value, err := d.string(maxPacketBytes)
		if err != nil {
			return ErrInvalid
		}
		key := string(name)
		if seen[key] {
			return ErrInvalid
		}
		seen[key] = true
		if key == "fsync@openssh.com" && string(value) == "1" {
			fsync = true
		}
	}
	if !fsync {
		return ErrInvalid
	}
	return nil
}

func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.closeOnce.Do(func() {
		c.closed.Store(true)
		if c.raw != nil {
			_ = c.raw.Close()
		}
		if c.channel != nil {
			_ = c.channel.Close()
		}
		if c.conn != nil {
			_ = c.conn.Close()
			_ = c.conn.Wait()
		}
		if c.globalDone != nil {
			<-c.globalDone
		}
		if c.incomingDone != nil {
			<-c.incomingDone
		}
		if c.requestsDone != nil {
			<-c.requestsDone
		}
		if c.stderrDone != nil {
			<-c.stderrDone
		}
		joinCancel(c.stopCancel, c.cancelDone)
		c.opMu.Lock()
		c.opMu.Unlock()
	})
	return nil
}

func joinCancel(stop func() bool, done chan struct{}) {
	if stop != nil && !stop() {
		<-done
	}
}

func (c *Client) lock() error {
	if c == nil {
		return ErrClosed
	}
	c.opMu.Lock()
	if c.closed.Load() {
		c.opMu.Unlock()
		return ErrClosed
	}
	if c.ctx.Err() != nil {
		c.opMu.Unlock()
		return c.ctx.Err()
	}
	return nil
}

func (c *Client) unlock() { c.opMu.Unlock() }

func (c *Client) exchange(typ byte, fields []byte) (byte, *decoder, error) {
	if c.closed.Load() || c.ctx.Err() != nil {
		return 0, nil, ErrClosed
	}
	if c.requestID == ^uint32(0) {
		return 0, nil, ErrInvalid
	}
	c.requestID++
	var request encoder
	request.u32(c.requestID)
	request = append(request, fields...)
	if err := writePacket(c.channel, typ, request); err != nil {
		return 0, nil, err
	}
	responseType, payload, err := readPacket(c.channel)
	if err != nil {
		return 0, nil, err
	}
	d := &decoder{data: payload}
	id, err := d.u32()
	if err != nil || id != c.requestID {
		return 0, nil, ErrInvalid
	}
	return responseType, d, nil
}

func numericAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.Contains(host, "%") || net.ParseIP(host) == nil {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n > 0 && net.JoinHostPort(net.ParseIP(host).String(), strconv.FormatUint(n, 10)) == address
}

func validUser(user string) bool {
	if len(user) == 0 || len(user) > 255 {
		return false
	}
	for _, r := range user {
		if r < 0x21 || r > 0x7e {
			return false
		}
	}
	return true
}

func validPath(value string) bool {
	if len(value) < 2 || len(value) > maxPathBytes || value[0] != '/' || strings.ContainsAny(value, "\\\x00") ||
		!utf8.ValidString(value) || path.Clean(value) != value || strings.ContainsFunc(value, unicode.IsControl) {
		return false
	}
	return true
}
