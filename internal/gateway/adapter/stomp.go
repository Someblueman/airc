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
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// ServeSTOMP owns the service's shared socket slots directly. The listener
// must not acquire them again. Each owner retains one individually acknowledged
// signed response under its selected fixed subscription.
func ServeSTOMP(parent context.Context, listener net.Listener, v *auth.Verifier, slots chan struct{}, perPrincipal int, options config.STOMP, realm string, d *dispatch.Dispatcher, sealer *auth.ResponseSealer, requests chan struct{}) error {
	if d == nil || sealer == nil || cap(requests) < 1 || listener == nil || v == nil || realm == "" || cap(slots) < 1 || cap(slots) > 256 || perPrincipal < 1 || perPrincipal > cap(slots) || len(options.Routes) < 1 || len(options.Routes) > 128 {
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
			c.serveSTOMP(options.Routes, realm, d, sealer, requests)
		})
	}
}

func (c *stompConnection) fail(message string) {
	_ = c.writeBefore(stompwire.Frame{Command: "ERROR", Headers: []stompwire.Header{{Key: "message", Value: message}}}, time.Time{}, true)
}
