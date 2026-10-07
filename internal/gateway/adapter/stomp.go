package adapter

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// ServeSTOMP owns the service's shared socket slots directly. The listener
// must not acquire them again. Signed dispatch is a separate prerequisite;
// this checkpoint accepts only heartbeat and orderly DISCONNECT frames.
func ServeSTOMP(parent context.Context, listener net.Listener, v *auth.Verifier, slots chan struct{}, perPrincipal int, options config.STOMP, realm string) error {
	if listener == nil || v == nil || realm == "" || cap(slots) < 1 || cap(slots) > 256 || perPrincipal < 1 || perPrincipal > cap(slots) || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid STOMP listener owner")
	}
	options.Routes = slices.Clone(options.Routes)
	for i := range options.Routes {
		options.Routes[i].Operations = slices.Clone(options.Routes[i].Operations)
	}
	ctx, cancel := context.WithCancel(parent)
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = listener.Close(); close(closed) })
	var owners sync.WaitGroup
	var principalMu sync.Mutex
	principals := make(map[string]int)
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
			c, err := openSTOMPConnection(ctx, conn, v, options.Routes, realm)
			if err != nil {
				return
			}
			principal := c.identity.ID()
			claimed := false
			defer func() {
				c.close()
				if claimed {
					principalMu.Lock()
					principals[principal]--
					if principals[principal] == 0 {
						delete(principals, principal)
					}
					principalMu.Unlock()
				}
			}()
			principalMu.Lock()
			allowed := principals[principal] < perPrincipal
			if allowed {
				principals[principal]++
				claimed = true
			}
			principalMu.Unlock()
			if !allowed {
				c.fail("STOMP connection capacity exceeded")
				return
			}
			if c.connected() != nil {
				return
			}
			c.serve()
		})
	}
}

func (c *stompConnection) fail(message string) {
	_ = c.writeBefore(stompwire.Frame{Command: "ERROR", Headers: []stompwire.Header{{Key: "message", Value: message}}}, time.Time{}, true)
}

func (c *stompConnection) serve() {
	for {
		f, err := c.readBefore(time.Time{})
		if err != nil {
			return
		}
		if f.Command == "" {
			continue
		}
		if f.Command != "DISCONNECT" {
			c.fail("unsupported STOMP command")
			return
		}
		if receipt, ok := f.Header("receipt"); ok {
			if len(receipt) == 0 || len(receipt) > 255 {
				c.fail("invalid STOMP receipt")
				return
			}
			_ = c.writeBefore(stompwire.Frame{Command: "RECEIPT", Headers: []stompwire.Header{{Key: "receipt-id", Value: receipt}}}, time.Time{}, true)
		}
		return
	}
}
