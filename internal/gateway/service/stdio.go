package service

import (
	"context"
	"errors"
	"fmt"
	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"net"
	"os"
	"sync"
	"time"
)

// RunStdio serves exactly one configured stdio listener over an already
// validated local pipe. It deliberately emits no readiness record: the
// parent process owns the pipe boundary and observes EOF or process exit.
func RunStdio(ctx context.Context, c config.Config, stream adapter.FramedStream) error {
	return runParentStream(ctx, c, stream, "stdio")
}

// RunSocketPair serves one configured socketpair listener over a validated,
// dedicated parent-owned endpoint, using the existing authenticated framing.
func RunSocketPair(ctx context.Context, c config.Config, stream adapter.FramedStream) error {
	return runParentStream(ctx, c, stream, "socketpair")
}

func runParentStream(ctx context.Context, c config.Config, stream adapter.FramedStream, protocol string) (failure error) {
	if stream == nil {
		return fmt.Errorf("missing %s stream", protocol)
	}
	defer stream.Close()
	if err := c.Validate(); err != nil {
		return err
	}
	if err := c.CheckFiles(); err != nil {
		return err
	}
	var listener config.Listener
	count := 0
	for _, candidate := range c.Listeners {
		if candidate.Enabled {
			count++
			if candidate.Protocol == protocol {
				listener = candidate
			}
		}
	}
	if count != 1 || listener.Protocol != protocol {
		return fmt.Errorf("serve-%s requires exactly one enabled %s listener", protocol, protocol)
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
	s, err := store.Open(ctx, c)
	if err != nil {
		return err
	}
	defer func() { failure = errors.Join(failure, s.Close()) }()
	d, err := dispatch.New(s, v, b, cursors, listener.Operations)
	if err != nil {
		return err
	}
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	requests := make(chan struct{}, c.Limits.Sessions)
	streamCtx, cancelStream := context.WithCancel(ctx)
	deliveryCtx, cancelDelivery := context.WithCancel(ctx)
	defer cancelStream()
	defer cancelDelivery()
	type outcome struct {
		stream bool
		err    error
	}
	results := make(chan outcome, 2)
	var work sync.WaitGroup
	work.Go(func() { results <- outcome{err: runner.Run(deliveryCtx)} })
	work.Go(func() {
		results <- outcome{stream: true, err: adapter.ServeFramedStream(streamCtx, stream, d, requests, false)}
	})
	first := <-results
	if first.stream && first.err == nil && ctx.Err() == nil {
		graceCtx, graceCancel := context.WithTimeout(ctx, 5*time.Second)
		for graceCtx.Err() == nil {
			pending, pendingErr := s.HasPending(graceCtx)
			if pendingErr != nil {
				if graceCtx.Err() == nil {
					first.err = pendingErr
				}
				break
			}
			if !pending {
				break
			}
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case second := <-results:
				first.err = errors.Join(first.err, second.err)
				timer.Stop()
				graceCancel()
				cancelDelivery()
				work.Wait()
				return stdioError(ctx, first.err)
			case <-timer.C:
			case <-graceCtx.Done():
			}
		}
		graceCancel()
		cancelDelivery()
		second := <-results
		first.err = errors.Join(first.err, second.err)
	} else {
		cancelStream()
		cancelDelivery()
		second := <-results
		first.err = errors.Join(first.err, second.err)
	}
	cancelStream()
	_ = stream.Close()
	work.Wait()
	if err := stdioError(ctx, first.err); err != nil {
		return err
	}
	return nil
}

func stdioError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		kept := make([]error, 0, len(many.Unwrap()))
		for _, child := range many.Unwrap() {
			if child = stdioError(ctx, child); child != nil {
				kept = append(kept, child)
			}
		}
		return errors.Join(kept...)
	}
	if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed)) {
		return nil
	}
	return err
}
