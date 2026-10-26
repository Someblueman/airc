// Package service owns configured ingress, delivery workers and journal lifetime.
package service

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

// readyRecord is the line Run writes once every listener is bound.
type readyRecord struct {
	Version   int    `json:"version"`
	State     string `json:"state"`
	Listeners []struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	} `json:"listeners"`
}

// Run binds only explicitly enabled instances. Any preparation/bind failure
// closes all owned resources and never emits a ready record. No upstream account
// is provisioned and native AIRC dials require a journal lease or read grant.
func Run(ctx context.Context, c config.Config, out io.Writer) (failure error) {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	for _, l := range c.Listeners {
		if t, _ := config.Lookup(l.Protocol); l.Enabled && t.Kind == config.Process {
			return t.ServeRefusal()
		}
	}
	b, err := backend.New(c)
	if err != nil {
		return err
	}
	defer b.Close()
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
	state := readyRecord{Version: 1, State: "ready"}
	instances := make([]instance, 0, enabled)
	defer func() {
		for _, i := range instances {
			i.release()
		}
	}()
	var owned handlers
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	o := &startup{ctx: ctx, work: workCtx, c: c, journal: s, slots: make(chan struct{}, c.Limits.Sessions), connections: make(chan struct{}, max(c.Limits.Connections, 1)), requests: make(chan struct{}, c.Limits.Requests()), certificates: certificates, owned: &owned}
	for _, l := range c.Listeners {
		if !l.Enabled {
			continue
		}
		var d *dispatch.Dispatcher
		if !config.NativeCarrier(l.Protocol) {
			if d, err = dispatch.ForListener(s, b, cursors, l); err != nil {
				return err
			}
		}
		start, ok := owners[l.Protocol]
		if !ok {
			return fmt.Errorf("listener %s: protocol %s has no service owner", l.ID, l.Protocol)
		}
		i, err := start(o, l, d)
		if err != nil {
			return err
		}
		instances = append(instances, i)
		state.Listeners = append(state.Listeners, struct {
			ID      string `json:"id"`
			Address string `json:"address"`
		}{l.ID, i.address})
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	results := make(chan error, len(instances)+1)
	var work sync.WaitGroup
	work.Go(func() { results <- runner.Run(workCtx) })
	for _, i := range instances {
		work.Go(func() { results <- i.serve() })
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
		if i.stop != nil {
			closing.Go(func() { i.stop(shutdown) })
		}
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
