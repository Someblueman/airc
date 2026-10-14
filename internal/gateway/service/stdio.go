package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/delivery"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

// RunStdio serves exactly one configured stdio listener over an already
// validated local pipe. It deliberately emits no readiness record: the
// parent process owns the pipe boundary and observes EOF or process exit.
func RunStdio(ctx context.Context, c config.Config, stream adapter.FramedStream) error {
	return runLocalStream(ctx, c, stream, "stdio")
}

// RunSocketPair serves one configured socketpair listener over a validated,
// dedicated parent-owned endpoint, using the existing authenticated framing.
func RunSocketPair(ctx context.Context, c config.Config, stream adapter.FramedStream) error {
	return runLocalStream(ctx, c, stream, "socketpair")
}

// RunInheritedUnixStream serves one inherited listener over a validated
// parent-provided Unix stream with authenticated frames and joined workers.
func RunInheritedUnixStream(ctx context.Context, c config.Config, stream adapter.FramedStream) error {
	return runLocalStream(ctx, c, stream, "inherited")
}

func runLocalStream(ctx context.Context, c config.Config, stream adapter.FramedStream, protocol string) (failure error) {
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
	listener, ok := soleListener(c, protocol)
	if !ok {
		return fmt.Errorf("serve-%s requires exactly one enabled %s listener", protocol, protocol)
	}
	var scpAuthor dispatch.RouteAuthor
	if protocol == "scp_inbox" {
		var err error
		scpAuthor, err = scpInboxAuthor(listener)
		if err != nil {
			return err
		}
	}
	// Plain author requests run under the listener's author policy; the
	// parent-owned pipe, FIFO pair, socket, serial line or SSH login is the
	// access gate.
	s, b, d, err := openListenerDispatch(ctx, c, listener)
	if err != nil {
		return err
	}
	defer b.Close()
	defer func() { failure = errors.Join(failure, s.Close()) }()
	runner, err := delivery.New(s, b, c.Limits.Workers)
	if err != nil {
		return err
	}
	requests := make(chan struct{}, c.Limits.Requests())
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
		var err error
		if protocol == "scp_inbox" {
			err = adapter.ServeSCPInbox(streamCtx, stream, d, *listener.SCPInbox, scpAuthor, requests)
		} else if protocol == "uart" {
			err = adapter.ServeUART(streamCtx, stream, d, requests)
		} else {
			err = adapter.ServeFramedStream(streamCtx, stream, d, requests)
		}
		results <- outcome{stream: true, err: err}
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
