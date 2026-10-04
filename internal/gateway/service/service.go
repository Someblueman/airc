// Package service owns configured ingress, delivery workers and journal lifetime.
package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// Run binds only explicitly enabled instances. Any preparation/bind failure
// closes all owned resources and never emits a ready record. No upstream account
// is provisioned and no dial occurs before a journal-authorized lease exists.
func Run(ctx context.Context, c config.Config, out io.Writer) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	v, err := auth.New(c)
	if err != nil {
		return err
	}
	b, err := backend.New(c)
	if err != nil {
		return err
	}
	cursors, err := auth.NewCursorCodec(c.CursorKeyFile)
	if err != nil {
		return err
	}
	certificates := map[string]tls.Certificate{}
	enabled := 0
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		enabled++
		if l.TLSCertFile != "" {
			pair, err := auth.ServerCertificate(l.TLSCertFile, l.TLSKeyFile)
			if err != nil {
				return err
			}
			certificates[l.ID] = pair
		}
	}
	if enabled == 0 {
		return errors.New("no enabled listeners")
	}
	s, err := store.Open(ctx, c)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	type instance struct {
		server   *http.Server
		listener net.Listener
	}
	instances := make([]instance, 0, enabled)
	defer func() {
		for _, i := range instances {
			_ = i.server.Close()
			_ = i.listener.Close()
		}
	}()
	state := struct {
		Version   int    `json:"version"`
		State     string `json:"state"`
		Listeners []struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		} `json:"listeners"`
	}{Version: 1, State: "ready"}
	slots := make(chan struct{}, c.Limits.Sessions)
	var owned handlers
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		d, err := dispatch.New(s, v, b, cursors, l.Operations)
		if err != nil {
			return err
		}
		server, err := adapter.HTTPServer(d, c.Limits.Sessions)
		if err != nil {
			return err
		}
		server.Handler = owned.wrap(server.Handler)
		server.BaseContext = func(net.Listener) context.Context { return workCtx }
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", l.Address)
		if err != nil {
			return err
		}
		bounded := limitedListener{Listener: listener, slots: slots}
		var transport net.Listener = bounded
		if pair, ok := certificates[l.ID]; ok {
			server.TLSConfig.Certificates = []tls.Certificate{pair}
			transport = tls.NewListener(bounded, server.TLSConfig.Clone())
		}
		instances = append(instances, instance{server, transport})
		state.Listeners = append(state.Listeners, struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}{l.ID, listener.Addr().String()})
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	results := make(chan error, len(instances)+1)
	var work sync.WaitGroup
	work.Go(func() { results <- runner.Run(workCtx) })
	for _, i := range instances {
		work.Go(func() { results <- i.server.Serve(i.listener) })
	}
	first := json.NewEncoder(out).Encode(state)
	if first == nil {
		select {
		case <-ctx.Done():
		case first = <-results:
		}
	}
	owned.stop()
	cancel()
	shutdown, finish := context.WithTimeout(context.Background(), 5*time.Second)
	defer finish()
	var closing sync.WaitGroup
	for _, i := range instances {
		closing.Go(func() { _ = i.server.Shutdown(shutdown); _ = i.server.Close() })
	}
	closing.Wait()
	work.Wait()
	owned.work.Wait()
	close(results)
	for err := range results {
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			first = errors.Join(first, err)
		}
	}
	return first
}
