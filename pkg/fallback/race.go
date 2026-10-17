package fallback

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"
)

// raced is one endpoint's connection attempt.
type raced struct {
	index int
	conn  *Conn
	err   error
}

// race connects to endpoints happy-eyeballs style: the first now, each next
// one when the latest has failed or has gone a stagger without progress. It
// returns the first established connection, the endpoints still untried (or
// abandoned when another won), and the attempts that failed, in the order
// they failed. A definite refusal ends the race. Attempted learns each
// failure as it happens.
func (c *Client) race(ctx context.Context, endpoints []Endpoint) (*Conn, []Endpoint, []Attempt, error) {
	results := make(chan raced, len(endpoints))
	progress := make(chan int, len(endpoints))
	cancels := make([]context.CancelFunc, 0, len(endpoints))
	finished := make([]bool, len(endpoints))
	pending := 0
	stagger := time.NewTimer(c.stagger())
	defer stagger.Stop()
	// The winner keeps its progress hook; once the race is over it is inert.
	var over atomic.Bool
	defer over.Store(true)
	start := func() {
		i := len(cancels)
		attempt, cancel := context.WithCancel(ctx)
		cancels = append(cancels, cancel)
		pending++
		stagger.Reset(c.stagger())
		go func() {
			conn, err := c.connect(attempt, endpoints[i], func() {
				if over.Load() {
					return
				}
				select {
				case progress <- i:
				default:
				}
			})
			results <- raced{index: i, conn: conn, err: err}
		}()
	}
	var winner *Conn
	var failed []Attempt
	var refusal error
	start()
	for pending > 0 && winner == nil && refusal == nil {
		select {
		case r := <-results:
			pending--
			finished[r.index] = true
			if r.err == nil {
				winner = r.conn
				break
			}
			e := endpoints[r.index]
			if c.Attempted != nil {
				c.Attempted(e, r.err)
			}
			if Definite(r.err) {
				refusal = r.err
				var definite *Refusal
				if !errors.As(refusal, &definite) {
					refusal = nativeRefusal(e, r.err)
				}
				break
			}
			failed = append(failed, Attempt{Endpoint: e, Err: r.err})
			// The latest attempt failed: start the next one now.
			if r.index == len(cancels)-1 && len(cancels) < len(endpoints) && ctx.Err() == nil {
				start()
			}
		case i := <-progress:
			if i == len(cancels)-1 {
				stagger.Reset(c.stagger())
			}
		case <-stagger.C:
			if len(cancels) < len(endpoints) && ctx.Err() == nil {
				start()
			}
		}
		if pending == 0 && winner == nil && refusal == nil && len(cancels) < len(endpoints) && ctx.Err() == nil {
			start()
		}
	}
	// Close the losers: cancel their dials, and close any that connected
	// after the winner.
	for i, cancel := range cancels {
		if !finished[i] {
			cancel()
		}
	}
	if pending > 0 {
		go func(n int) {
			for range n {
				if r := <-results; r.conn != nil {
					_ = r.conn.Close()
				}
			}
		}(pending)
	}
	defer func() {
		for i, cancel := range cancels {
			if finished[i] {
				cancel()
			}
		}
	}()
	var rest []Endpoint
	for i, e := range endpoints {
		if i >= len(cancels) || !finished[i] {
			rest = append(rest, e)
		}
	}
	return winner, rest, failed, refusal
}

// connect establishes a connection to e within its connect timeout, calling
// progress when the connection opens and whenever bytes arrive on it.
func (c *Client) connect(ctx context.Context, e Endpoint, progress func()) (*Conn, error) {
	if NativeCarrier(e.Name) {
		ctx, cancel := context.WithTimeout(ctx, c.nativeConnectTimeout())
		defer cancel()
		client, err := c.dialNative(ctx, e, progress)
		if err != nil {
			return nil, err
		}
		return &Conn{Endpoint: e, native: client}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, c.connectTimeout())
	defer cancel()
	var conn gatewayConn
	var err error
	switch e.Name {
	case "http":
		conn, err = c.dialHTTP(ctx, e, progress)
	case "http3":
		conn, err = c.dialHTTP3(ctx, e, progress)
	case "websocket":
		conn, err = c.dialWebSocket(ctx, e, progress)
	case "framed", "unix":
		conn, err = c.dialFramed(ctx, e, progress)
	default:
		err = fmt.Errorf("carrier %s is not supported by this client", e.Name)
	}
	if err != nil {
		return nil, err
	}
	return &Conn{Endpoint: e, gateway: conn}, nil
}

// watched reports reads on a connection that is still being set up, so the
// race can tell a slow but live endpoint from one that dropped the packets.
type watched struct {
	net.Conn
	progress func()
}

func (w *watched) Read(b []byte) (int, error) {
	n, err := w.Conn.Read(b)
	if n > 0 && w.progress != nil {
		w.progress()
	}
	return n, err
}

// dial opens a TCP or Unix connection, reporting progress on it.
func (c *Client) dial(ctx context.Context, network, address string, progress func()) (net.Conn, error) {
	// ctx carries the endpoint's own connect timeout (native or gateway).
	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if progress == nil {
		return conn, nil
	}
	progress()
	return &watched{Conn: conn, progress: progress}, nil
}
