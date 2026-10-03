package server

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"net"
	"runtime/debug"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func (c *session) readLoop() {
	defer c.server.remove(c, "connection closed")
	deadline := c.client.ConnectedAt.Add(c.server.cfg.RegistrationTimeout)
	_ = c.conn.SetDeadline(deadline)
	if conn, ok := c.conn.(*tls.Conn); ok {
		if err := conn.Handshake(); err != nil {
			return
		}
	}
	_ = c.conn.SetWriteDeadline(time.Time{})
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 1024), protocol.MaxLineLength+2)
	for scanner.Scan() {
		line := scanner.Text()
		command, err := protocol.Parse(line)
		if err != nil {
			c.server.logger.Warn("protocol_error", "id", c.client.ID, "error", err.Error())
			c.server.numeric(c, "417", nil, "malformed or oversized command")
			return
		}
		if !c.server.handleRecovering(c, command) {
			return
		}
		c.server.mu.Lock()
		registered := c.registered
		c.server.mu.Unlock()
		if registered {
			deadline = time.Now().Add(c.server.cfg.ReadTimeout)
		}
		_ = c.conn.SetReadDeadline(deadline)
		select {
		case <-c.done:
			return
		default:
		}
	}
	if err := scanner.Err(); err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			c.server.logger.Info("client_timeout", "nick", c.client.Nick)
		} else {
			c.server.logger.Debug("client_read_error", "nick", c.client.Nick, "error", err.Error())
		}
	}
}

// handleRecovering confines a handler panic to the session that triggered it;
// the daemon is shared, so one bad command must not disconnect everyone.
func (s *Server) handleRecovering(client *session, command protocol.Command) bool {
	return s.recovering(client, command.Name, func() { s.handle(client, command) })
}

func (s *Server) recovering(client *session, name string, handler func()) (ok bool) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.logger.Error("handler_panic", "id", client.client.ID, "command", name, "panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
			ok = false
		}
	}()
	handler()
	return true
}

func (c *session) writeLoop() {
	ticker := time.NewTicker(c.server.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-c.overload:
			c.finishOverload()
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, c.lastPong.Load())) > c.server.cfg.ReadTimeout {
				c.close()
				return
			}
			if !c.enqueue(fmt.Sprintf(":server PING :%d\r\n", time.Now().Unix())) {
				if c.overloaded.Load() {
					c.finishOverload()
				}
				return
			}
		case line := <-c.out:
			if c.overloaded.Load() {
				c.finishOverload()
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if c.overloaded.Load() {
				c.finishOverload()
				return
			}
			if _, err := c.conn.Write([]byte(line)); err != nil {
				if c.overloaded.Load() {
					c.finishOverload()
				} else {
					c.close()
				}
				return
			}
			c.outBytes.Add(-int64(len(line)))
		}
	}
}
