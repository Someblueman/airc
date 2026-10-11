package adapter

import (
	"context"
	"errors"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/stompwire"
)

// ServeSTOMP owns the service's shared socket slots directly. The listener
// must not acquire them again. Each SEND is a plain contract request naming
// its author, or none after an account.identify on the same connection; a
// connection subscribed to its route's response destination gets one
// individually acknowledged plain JSON result per request.
func ServeSTOMP(parent context.Context, listener net.Listener, slots chan struct{}, options config.STOMP, d *dispatch.Dispatcher, requests chan struct{}) error {
	if d == nil || !d.AuthorAccess() || cap(requests) < 1 || listener == nil || cap(slots) < 1 || cap(slots) > 256 || len(options.Routes) < 1 || len(options.Routes) > 128 {
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
			c, err := openSTOMPConnection(ctx, conn, d)
			if err != nil {
				return
			}
			defer c.close()
			if c.connected() != nil {
				return
			}
			c.serveSTOMP(options.Routes, d, requests)
		})
	}
}

func (c *stompConnection) fail(message string) {
	_ = c.writeBefore(stompwire.Frame{Command: "ERROR", Headers: []stompwire.Header{{Key: "message", Value: message}}}, time.Time{}, true)
}
