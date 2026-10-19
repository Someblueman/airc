package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/tokenfmt"
)

// clientTLS runs a TLS handshake over conn as tls.Dialer would: the server
// name defaults to the address's host.
func clientTLS(ctx context.Context, conn net.Conn, cfg *tls.Config, address string) (net.Conn, error) {
	if cfg.ServerName == "" {
		cfg = cfg.Clone()
		cfg.ServerName, _, _ = net.SplitHostPort(address)
	}
	secure := tls.Client(conn, cfg)
	if err := secure.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return secure, nil
}

func (c *Client) connect(ctx context.Context) (net.Conn, *bufio.Scanner, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	switch {
	case c.cfg.DialContext != nil:
		conn, err = c.cfg.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
		if err == nil && c.cfg.TLSConfig != nil {
			conn, err = clientTLS(ctx, conn, c.cfg.TLSConfig, c.cfg.Addr)
		}
	case c.cfg.TLSConfig != nil:
		secure := tls.Dialer{NetDialer: &dialer, Config: c.cfg.TLSConfig}
		conn, err = secure.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
	default:
		conn, err = dialer.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("dial airc: %w", err)
	}
	if c.cfg.TLSConfig == nil {
		if addr, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !addr.IP.IsLoopback() {
			_ = conn.Close()
			return nil, nil, errors.New("remote TCP connections require TLS; use TLS or a loopback SSH tunnel")
		}
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	if !c.setConn(conn) {
		_ = conn.Close()
		return nil, nil, net.ErrClosed
	}
	c.setEphemeral(false)
	c.setAccountID("")
	c.setFeatures(nil)
	// Registration is one write: every line, the SASL login included, goes
	// in the connection's first record. aircd reads its lines in order, so
	// nothing waits on a reply, and a peer refusing the first line has read
	// the rest by then, so its close cannot reset the connection before the
	// refusal is read.
	var flight strings.Builder
	if c.cfg.GatewayToken != "" {
		// First, so every later command is attributed to the client's source.
		flight.WriteString("WEBIRC " + c.cfg.Source + " :" + c.cfg.GatewayToken + "\r\n")
	}
	if c.cfg.AccessToken != "" {
		flight.WriteString("PASS :" + c.cfg.AccessToken + "\r\n")
	}
	deadline := time.Now().Add(c.cfg.ReadTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = conn.SetReadDeadline(deadline)
	scanner := newScanner(conn)
	// pendingLogin holds until the server confirms the SASL login, whose
	// replies arrive before registration completes.
	pendingLogin := false
	if c.cfg.IdentityToken != "" {
		if !validIdentityToken(c.cfg.IdentityToken) {
			_ = conn.Close()
			return nil, nil, errors.New("invalid account credential")
		}
		if c.cfg.CreateAccount {
			line, err := commandLine("REGISTER", []string{c.currentNick()}, c.cfg.IdentityToken)
			if err != nil {
				_ = conn.Close()
				return nil, nil, err
			}
			flight.WriteString(line)
		} else {
			flight.WriteString(c.saslLogin())
			pendingLogin = true
		}
	}
	if c.cfg.Ephemeral {
		// Before NICK and USER, so the mode applies to registration itself.
		flight.WriteString("EPHEMERAL\r\n")
	}
	nick := c.currentNick()
	c.mu.Lock()
	resume := c.resume
	c.mu.Unlock()
	if resume != "" && !c.cfg.Ephemeral {
		// Only a server that issued the secret (767) knows RESUME, so an
		// older server is never asked.
		line, err := commandLine("RESUME", []string{nick, resume}, "")
		if err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		flight.WriteString(line)
	}
	for _, command := range [][]string{{"NICK", nick}, {"USER", c.cfg.Username, "0", "*"}} {
		trailing := ""
		if command[0] == "USER" {
			trailing = c.cfg.RealName
		}
		line, err := commandLine(command[0], command[1:], trailing)
		if err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		flight.WriteString(line)
	}
	if err := c.writeLine(flight.String()); err != nil {
		c.clearConn(conn)
		_ = conn.Close()
		return nil, nil, err
	}
	for scanner.Scan() {
		command, err := c.dispatch(ctx, scanner.Text())
		if err != nil {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, err
		}
		if pendingLogin {
			confirmed, err := saslReply(command)
			if err != nil {
				c.clearConn(conn)
				_ = conn.Close()
				return nil, nil, err
			}
			pendingLogin = !confirmed
		}
		if command.Name == "464" || command.Name == "498" || command.Name == "437" || command.Name == "451" || command.Name == "421" && (c.cfg.IdentityToken != "" || c.cfg.AccessToken != "") || (command.Name == "461" || command.Name == "462") && c.cfg.GatewayToken != "" {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, &RejectedError{Code: command.Name, Message: "registration rejected: " + command.Trailing, Throttled: throttled(command.Params)}
		}
		if command.Name == "766" {
			c.setEphemeral(true)
		}
		if command.Name == "767" {
			secret, _ := command.Param(1)
			if protocol.ValidMessageID(secret) {
				c.setResume(secret)
			}
		}
		if command.Name == "005" {
			c.addFeatures(command.Params)
		}
		if command.Name == "433" || command.Name == "432" || command.Name == "465" {
			c.clearConn(conn)
			_ = conn.Close()
			if command.Name == "465" {
				return nil, nil, fmt.Errorf("nickname %q is banned: %s", nick, command.Trailing)
			}
			if command.Name == "433" {
				return nil, nil, &NickInUseError{Nick: nick}
			}
			return nil, nil, fmt.Errorf("nickname %q is invalid", nick)
		}
		if command.Name == "001" {
			if pendingLogin {
				c.clearConn(conn)
				_ = conn.Close()
				return nil, nil, errors.New("server registered the connection without confirming its login")
			}
			if err := c.rejoin(); err != nil {
				c.clearConn(conn)
				_ = conn.Close()
				return nil, nil, err
			}
			_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
			return conn, scanner, nil
		}
	}
	err = scanner.Err()
	if err == nil {
		err = fmt.Errorf("server closed connection before registration: %w", io.ErrUnexpectedEOF)
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

func validIdentityToken(token string) bool { return tokenfmt.Valid(token) }
