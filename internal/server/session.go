package server

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func (c *session) readLoop() {
	defer c.server.remove(c, "connection closed")
	_ = c.conn.SetReadDeadline(time.Now().Add(c.server.cfg.ReadTimeout))
	scanner := bufio.NewScanner(c.conn)
	scanner.Buffer(make([]byte, 1024), protocol.MaxLineLength+2)
	for scanner.Scan() {
		_ = c.conn.SetReadDeadline(time.Now().Add(c.server.cfg.ReadTimeout))
		line := scanner.Text()
		command, err := protocol.Parse(line)
		if err != nil {
			c.server.logger.Warn("protocol_error", "id", c.client.ID, "error", err.Error())
			c.server.numeric(c, "417", nil, "malformed or oversized command")
			return
		}
		c.server.handle(c, command)
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

func (c *session) writeLoop() {
	ticker := time.NewTicker(c.server.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
			if time.Since(time.Unix(0, c.lastPong.Load())) > c.server.cfg.ReadTimeout {
				c.close()
				return
			}
			if !c.enqueue(fmt.Sprintf(":server PING :%d\r\n", time.Now().Unix())) {
				return
			}
		case line := <-c.out:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := c.conn.Write([]byte(line)); err != nil {
				c.close()
				return
			}
		}
	}
}
