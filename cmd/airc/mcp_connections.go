package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// Each lease has exclusive ownership of its client's event stream. Wait calls
// close on release because their observation subscriptions are request-specific.
type agentConnection struct {
	mu     sync.Mutex
	client *irc.Client
	idle   *time.Timer
}

func (a *mcpAdapter) connections() chan *agentConnection {
	a.poolOnce.Do(func() {
		a.pool = make(chan *agentConnection, 4)
		a.pool <- &agentConnection{}
	})
	return a.pool
}

func (a *mcpAdapter) runCommand(ctx context.Context, args []string, input string, waiting bool, stdout, stderr io.Writer) (err error) {
	pool := a.connections()
	var conn *agentConnection
	select {
	case conn = <-pool:
	default:
		conn = &agentConnection{}
	}
	if err := ctx.Err(); err != nil {
		pool <- conn
		return err
	}
	conn.mu.Lock()
	if conn.idle != nil {
		conn.idle.Stop()
		conn.idle = nil
	}
	defer func() {
		if conn.client != nil {
			if err != nil || waiting || ctx.Err() != nil {
				conn.client.Close()
				conn.client = nil
			} else {
				c := conn.client
				var timer *time.Timer
				timer = time.AfterFunc(time.Minute, func() {
					conn.mu.Lock()
					defer conn.mu.Unlock()
					if conn.idle == timer && conn.client == c {
						c.Close()
						conn.client = nil
						conn.idle = nil
					}
				})
				conn.idle = timer
			}
		}
		conn.mu.Unlock()
		pool <- conn
	}()
	if len(args) == 0 {
		return errors.New("missing agent command")
	}
	switch args[0] {
	case "send":
		return runSendSession(ctx, conn, args[1:], strings.NewReader(input), stdout, stderr)
	case "check":
		return runCheckSession(ctx, conn, args[1:], stdout, stderr)
	case "context":
		return runContextSession(ctx, conn, args[1:], stdout, stderr)
	case "directory":
		return runDirectoryCommandSession(ctx, conn, "directory", args[1:], stdout, stderr)
	case "thread":
		if len(args) < 2 {
			return errors.New("thread ID required")
		}
		return runHistorySession(ctx, conn, append([]string{"thread:" + args[1]}, args[2:]...), stdout, stderr)
	case "search":
		return runSearchSession(ctx, conn, args[1:], stdout, stderr)
	case "follow", "unfollow":
		return runFollowSession(ctx, conn, args[0], args[1:], stdout, stderr)
	case "correct", "retract", "prepare", "waiting":
		return runChatCommandSession(ctx, conn, args[0], args[1:], stdout, stderr)
	default:
		return errors.New("unsupported agent command")
	}
}

func (a *mcpAdapter) closeConnections() {
	pool := a.connections()
	// Run has stopped accepting calls and the lifetime context is cancelled.
	// Acquire all permits so every active lease has returned before draining.
	for range cap(a.slots) {
		a.slots <- struct{}{}
	}
	for len(pool) > 0 {
		conn := <-pool
		conn.mu.Lock()
		if conn.idle != nil {
			conn.idle.Stop()
			conn.idle = nil
		}
		if conn.client != nil {
			conn.client.Close()
			conn.client = nil
		}
		conn.mu.Unlock()
	}
}
