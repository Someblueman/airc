package main

import (
	"context"
	"errors"
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
	return awaitEventAs(ctx, client.Events(), client.Done(), timeout, "server disconnected while "+what, "connection closed while "+what, timedOut, handle)
}

// awaitReply is awaitEvent for a request bounded only by ctx whose server error
// numerics end it: it hands handle every other event. lost is the whole error
// text for a lost connection. Buffered replies remain usable after closure.
func awaitReply(ctx context.Context, client *irc.Client, lost string, handle func(irc.Event) (done bool, err error)) error {
	return awaitEventAs(ctx, client.Events(), nil, 0, lost, lost, "", func(event irc.Event) (bool, error) {
		if err := serverError(event); err != nil {
			return true, err
		}
		return handle(event)
	})
}

// awaitEventAs is awaitEvent with the full text of its two disconnect errors:
// eof when the event stream ends, closed when closing is signalled. A nil
// closing channel lets a request drain buffered responses before seeing EOF.
func awaitEventAs(ctx context.Context, events <-chan irc.Event, closing <-chan struct{}, timeout time.Duration, eof, closed, timedOut string, handle func(irc.Event) (done bool, err error)) error {
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
		case event, ok := <-events:
			if !ok {
				return lost(eof)
			}
			if done, err := handle(event); done || err != nil {
				return err
			}
		case <-ctx.Done():
			return ctx.Err()
		case <-expired:
			return timeoutError(timedOut)
		case <-closing:
			return lost(closed)
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

// superviseSessions keeps session running over ephemeral connections until ctx
// ends, then returns nil. session reports whether it got as far as streaming;
// an up session resets the backoff. A failure before the first session is
// established is returned, as is anything outage returns for a later loss, which
// is otherwise retried after a backoff wait. While native IRC is down after an
// outage, bridge (when set) runs until back closes: a native connection has
// registered again.
func superviseSessions(ctx context.Context, opt options, session func(ctx context.Context, client *irc.Client, first bool) (up bool, err error), outage func(error) error, bridge func(ctx context.Context, back <-chan struct{}) error) error {
	retry := newBackoff(watchMinBackoff, watchMaxBackoff, false)
	established := false
	var ready *irc.Client // a connection that registered while bridged
	defer func() {
		if ready != nil {
			ready.Close() // ctx ended before it was used
		}
	}()
	for ctx.Err() == nil {
		var up bool
		client, err := ready, error(nil)
		ready = nil
		if client == nil {
			client, err = dialSession(ctx, opt, established)
		}
		if err == nil {
			up, err = session(ctx, client, !established)
			client.Close()
		}
		if ctx.Err() != nil {
			return nil
		}
		if up {
			established = true
			retry.reset()
		}
		if !established {
			return err
		}
		if err := outage(err); err != nil {
			return err
		}
		if bridge != nil {
			if ready = bridgeUntilNative(ctx, opt, bridge, retry); ready == nil {
				return nil
			}
			continue
		}
		if !retry.wait(ctx) {
			return nil
		}
	}
	return nil
}

// connectionLost is why client's connection ended, for the outage notice.
func connectionLost(client *irc.Client) error {
	if errors.Is(client.Err(), irc.ErrStalled) {
		return irc.ErrStalled
	}
	return errors.New("server closed the connection")
}

// dialSession registers an ephemeral native connection. After the first
// session each attempt has the native dial budget, so a port that went
// silent is retried rather than waited on.
func dialSession(ctx context.Context, opt options, bounded bool) (*irc.Client, error) {
	cfg, err := dialConfig(opt)
	if err != nil {
		return nil, err
	}
	cfg.Ephemeral = true
	if bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, nativeDialBudget)
		defer cancel()
	}
	return irc.DialContext(ctx, cfg)
}

// bridgeUntilNative runs bridge while redialling native IRC with backoff, and
// returns the native connection once one registers, or nil when ctx ends.
func bridgeUntilNative(ctx context.Context, opt options, bridge func(context.Context, <-chan struct{}) error, retry *backoff) *irc.Client {
	back := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = bridge(ctx, back) // it reports its own trouble; native IRC is retried regardless
	}()
	defer func() {
		close(back)
		<-done
	}()
	for retry.wait(ctx) {
		if client, err := dialSession(ctx, opt, true); err == nil {
			return client
		}
	}
	return nil
}
