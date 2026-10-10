package adapter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/amqpwire"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// ServeAMQP owns admission directly through the service's shared socket slots.
// Its listener must not also acquire those same slots. Requests are plain
// contract JSON attributed to the SASL-authenticated route principal.
func ServeAMQP(parent context.Context, listener net.Listener, v *auth.Verifier, slots chan struct{}, perPrincipal int, options config.AMQP, d *dispatch.Dispatcher, requests chan struct{}) error {
	if d == nil || cap(requests) < 1 || listener == nil || v == nil || cap(slots) < 1 || cap(slots) > 256 || perPrincipal < 1 || perPrincipal > cap(slots) || len(options.Routes) < 1 || len(options.Routes) > 128 {
		return errors.New("invalid AMQP listener owner")
	}
	options.Routes = slices.Clone(options.Routes)
	for i := range options.Routes {
		options.Routes[i].Operations = slices.Clone(options.Routes[i].Operations)
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	container := "airc-gateway/" + hex.EncodeToString(nonce[:])
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
			c, err := openAMQPConnection(ctx, conn, v, options.Routes, container)
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
				c.reject("amqp:resource-limit-exceeded")
				return
			}
			c.serveRequests(options, d, requests)
		})
	}
}

// A locally initiated Close disables heartbeats, then gives the peer a bounded
// opportunity to acknowledge Close. No frame is sent after the closing frame.
func (c *amqpConnection) reject(condition string) {
	p := amqpwire.Performative{Kind: 0x18}
	if condition != "" {
		p.Fields = []amqpwire.Value{amqpError(condition)}
	}
	if c.write(p) != nil {
		return
	}
	if c.conn.SetReadDeadline(time.Now().Add(amqpDeadline)) != nil {
		return
	}
	for {
		frame, err := amqpwire.ReadFrame(c.conn, amqpwire.MaxFrameBytes)
		if err != nil || frame.Channel != 0 {
			return
		}
		p, err := amqpwire.DecodePerformative(frame)
		if err != nil || p.Kind == 0x18 {
			return
		}
	}
}

func amqpError(condition string) amqpwire.Value {
	return amqpwire.Value{Descriptor: &amqpwire.Value{Code: 0x80, Number: 0x1d}, Children: []amqpwire.Value{{Code: 0xc0, Children: []amqpwire.Value{{Code: 0xa3, Data: []byte(condition)}}}}}
}
func (c *amqpConnection) endSession(condition string) {
	if c.write(amqpwire.Performative{Kind: 0x17, Fields: []amqpwire.Value{amqpError(condition)}}) == nil {
		c.reject("")
	}
}
