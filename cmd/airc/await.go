package main

import (
	"context"
	"io"
	"math/rand/v2"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

// closeOnCancel closes client when ctx ends; call the result to stop watching.
func closeOnCancel(ctx context.Context, client *irc.Client) func() bool {
	return context.AfterFunc(ctx, func() { _ = client.Close() })
}

// disconnectError reports a connection lost mid-request in the command's own
// words, while failure() still classifies it as a retryable server_unavailable.
type disconnectError string

func (e disconnectError) Error() string { return string(e) }
func (e disconnectError) Unwrap() error { return io.EOF }

// timeoutError reports a request the server never answered, while failure()
// still classifies it as a retryable timeout.
type timeoutError string

func (e timeoutError) Error() string { return string(e) }
func (e timeoutError) Unwrap() error { return context.DeadlineExceeded }

// awaitEvent feeds client events to handle until it reports done or fails.
// what completes the disconnect errors ("... while "+what); timedOut is the
// error after timeout, and a zero timeout leaves only ctx to bound the wait.
// A cancelled ctx wins over a disconnect.
func awaitEvent(ctx context.Context, client *irc.Client, timeout time.Duration, what, timedOut string, handle func(irc.Event) (done bool, err error)) error {
	var expired <-chan time.Time
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		expired = timer.C
	}
	lost := func(message string) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return disconnectError(message)
	}
	for {
		select {
		case event, ok := <-client.Events():
			if !ok {
				return lost("server disconnected while " + what)
			}
			if done, err := handle(event); done || err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-expired:
			return timeoutError(timedOut)
		case <-client.Done():
			return lost("connection closed while " + what)
		}
	}
}

// backoff paces reconnect attempts: delays double from min up to max, and with
// jitter each wait is a random time between half the delay and the delay.
type backoff struct {
	min, max, delay time.Duration
	jitter          bool
}

func newBackoff(min, max time.Duration, jitter bool) *backoff {
	return &backoff{min: min, max: max, delay: min, jitter: jitter}
}

func (b *backoff) reset() { b.delay = b.min }

// wait sleeps for the current delay, then doubles it. It reports false if ctx
// ended first.
func (b *backoff) wait(ctx context.Context) bool {
	d := b.delay
	if b.jitter {
		d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	b.delay = min(b.delay*2, b.max)
	return true
}
