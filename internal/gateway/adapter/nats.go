package adapter

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"net"
	"runtime"
	"slices"
	"strconv"
	"sync"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/natswire"
)

// ServeNATS owns shared socket admission directly, including preauthentication
// sockets. listener must accept raw TCP sockets: INFO precedes the TLS upgrade.
// This prerequisite serves controls only; enabled service/dispatch remains absent.
func ServeNATS(parent context.Context, listener net.Listener, security *tls.Config, v *auth.Verifier, slots chan struct{}, perPrincipal int, options config.NATS, realm string) error {
	if listener == nil || security == nil || v == nil || realm == "" || !natsMetadata(realm) || cap(slots) < 1 || cap(slots) > 256 || perPrincipal < 1 || perPrincipal > cap(slots) || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid NATS listener owner")
	}
	host, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		return errors.New("NATS listener requires a TCP address")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return errors.New("invalid NATS listener port")
	}
	info := natsInfo{ServerID: "gateway-" + rand.Text(), ServerName: realm, Version: "1.0.0", Go: runtime.Version(), Host: host, Port: n, MaxPayload: natswire.MaxPayload, Proto: 1, AuthRequired: true, TLSRequired: true}
	options.Routes = slices.Clone(options.Routes)
	for i := range options.Routes {
		options.Routes[i].Operations = slices.Clone(options.Routes[i].Operations)
	}
	security = security.Clone()
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
			c, err := openNATSConnection(ctx, conn, security, v, options.Routes, info)
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
				c.fail()
				return
			}
			if c.connected() != nil {
				return
			}
			c.serveNATSControls()
		})
	}
}
