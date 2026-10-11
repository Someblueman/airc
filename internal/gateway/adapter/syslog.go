package adapter

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
	"net"
	"sync"
	"time"
)

// ServeSyslog owns shared socket slots directly, including unauthenticated TLS
// peers. A pinned client certificate (or, for signed syslog, the record
// origin's signature) selects a grant, and its records post as the grant's
// bound author. The listener must use the authenticator's TLSConfig and no slot wrapper.
// There is no wire acknowledgement; admitted sends belong to durable workers.
func ServeSyslog(parent context.Context, listener net.Listener, a *auth.SyslogAuthenticator, signed *auth.SignedSyslogAuthenticator, slots chan struct{}, perAuthor int, realm string, d *dispatch.Dispatcher, requests chan struct{}) error {
	if listener == nil || a == nil || d == nil || cap(slots) < 1 || cap(slots) > 256 || perAuthor < 1 || perAuthor > cap(slots) || cap(requests) < 1 || realm == "" {
		return errors.New("invalid TLS syslog listener owner")
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	var owners sync.WaitGroup
	var mu sync.Mutex
	authors := map[string]int{}
	defer func() {
		cancel()
		if !stop() {
			<-closed
		}
		_ = listener.Close()
		owners.Wait()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
		default:
			closeMQTTConnection(conn)
			continue
		}
		owners.Go(func() {
			defer func() { <-slots }()
			claimed := ""
			finished := make(chan struct{})
			interrupt := context.AfterFunc(ctx, func() { closeMQTTConnection(conn); close(finished) })
			defer func() {
				if !interrupt() {
					<-finished
				}
				closeMQTTConnection(conn)
				if claimed != "" {
					mu.Lock()
					authors[claimed]--
					if authors[claimed] == 0 {
						delete(authors, claimed)
					}
					mu.Unlock()
				}
			}()
			tlsConn, ok := conn.(*tls.Conn)
			if !ok || conn.SetDeadline(time.Now().Add(3*time.Second)) != nil {
				return
			}
			author, target, err := a.Authenticate(ctx, tlsConn)
			if err != nil {
				return
			}
			// Each grant's author holds at most perAuthor connections.
			scope := author.Author.Scope()
			mu.Lock()
			allowed := authors[scope] < perAuthor
			if allowed {
				authors[scope]++
				claimed = scope
			}
			mu.Unlock()
			if !allowed {
				return
			}
			serveSyslogRecords(ctx, conn, author, target, realm, d, requests, signed)
		})
	}
}

func serveSyslogRecords(ctx context.Context, conn net.Conn, author auth.RouteAuthor, target core.Target, realm string, d *dispatch.Dispatcher, requests chan struct{}, signed *auth.SignedSyslogAuthenticator) {
	var buffer *auth.SignedSyslogBuffer
	if signed != nil {
		var err error
		buffer, err = auth.NewSignedSyslogBuffer(signed)
		if err != nil {
			return
		}
	}
	reader := bufio.NewReaderSize(conn, 4096)
	for ctx.Err() == nil {
		idle := time.Now().Add(30 * time.Second)
		if buffer != nil && !buffer.Deadline().IsZero() && buffer.Deadline().Before(idle) {
			idle = buffer.Deadline()
		}
		if conn.SetReadDeadline(idle) != nil {
			return
		}
		// Idle time only applies before the first byte. A partial frame has one
		// absolute three-second deadline that incoming fragments cannot renew.
		if _, err := reader.Peek(1); err != nil {
			return
		}
		deadline := time.Now().Add(3 * time.Second)
		if buffer != nil && !buffer.Deadline().IsZero() && buffer.Deadline().Before(deadline) {
			deadline = buffer.Deadline()
		}
		if conn.SetReadDeadline(deadline) != nil {
			return
		}
		record, err := syslogwire.Read(reader)
		if err != nil || !time.Now().Before(deadline) || ctx.Err() != nil {
			return
		}
		if buffer != nil {
			batch, err := buffer.Push(record.Raw, time.Now())
			if err != nil {
				return
			}
			if batch != nil {
				if err := admitSignedSyslog(ctx, d, batch, requests); err != nil {
					return
				}
			}
			continue
		}
		if _, err = admitSyslog(ctx, d, author, realm, target, record, requests); err != nil {
			return
		}
	}
}
