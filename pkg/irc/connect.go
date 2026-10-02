package irc

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/Someblueman/airc/internal/protocol"
	"net"
	"time"
)

func (c *Client) connect(ctx context.Context) (net.Conn, *bufio.Scanner, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if c.cfg.TLSConfig != nil {
		secure := tls.Dialer{NetDialer: &dialer, Config: c.cfg.TLSConfig}
		conn, err = secure.DialContext(ctx, c.cfg.Network, c.cfg.Addr)
	} else {
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
	c.setFeatures(nil)
	if c.cfg.AccessToken != "" {
		if err := c.writeLine("PASS :" + c.cfg.AccessToken + "\r\n"); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
	deadline := time.Now().Add(c.cfg.ReadTimeout)
	if until, ok := ctx.Deadline(); ok && until.Before(deadline) {
		deadline = until
	}
	_ = conn.SetReadDeadline(deadline)
	scanner := newScanner(conn)
	if c.cfg.IdentityToken != "" && !c.cfg.CreateAccount {
		if !validIdentityToken(c.cfg.IdentityToken) {
			_ = conn.Close()
			return nil, nil, errors.New("invalid account credential")
		}
		if err := c.loginSASL(ctx, scanner); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
	if c.cfg.IdentityToken != "" && c.cfg.CreateAccount {
		if !validIdentityToken(c.cfg.IdentityToken) {
			_ = conn.Close()
			return nil, nil, errors.New("invalid account credential")
		}
		name := "REGISTER"
		line, err := commandLine(name, []string{c.currentNick()}, c.cfg.IdentityToken)
		if err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
		if err := c.writeLine(line); err != nil {
			_ = conn.Close()
			return nil, nil, err
		}
	}
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
	for scanner.Scan() {
		command, err := c.dispatch(ctx, scanner.Text())
		if err != nil {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, err
		}
		if command.Name == "464" || command.Name == "498" || command.Name == "437" || command.Name == "451" || command.Name == "421" && (c.cfg.IdentityToken != "" || c.cfg.AccessToken != "") {
			c.clearConn(conn)
			_ = conn.Close()
			return nil, nil, fmt.Errorf("registration rejected: %s", command.Trailing)
		}
		if command.Name == "766" {
			c.setEphemeral(true)
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
			_ = conn.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
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
