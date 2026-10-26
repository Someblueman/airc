//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/fallback"
	"github.com/Someblueman/airc/pkg/irc"
)

// The client library's hedged follow (pkg/fallback, FollowHedged) through
// this gateway, run in process by followGateway.

// stallProxy forwards TCP to target until stall, after which it forwards
// nothing in either direction and closes nothing, as a dead NAT path does.
// accepted counts the connections it took.
type stallProxy struct {
	listener net.Listener
	accepted atomic.Int32
	mu       sync.Mutex
	stalled  bool
}

func newStallProxy(t *testing.T, target string) *stallProxy {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &stallProxy{listener: l}
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			client, err := l.Accept()
			if err != nil {
				return
			}
			p.accepted.Add(1)
			go func() {
				defer client.Close()
				upstream, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer upstream.Close()
				done := make(chan struct{}, 2)
				pipe := func(dst, src net.Conn) {
					buf := make([]byte, 32<<10)
					for {
						n, err := src.Read(buf)
						if p.isStalled() {
							<-t.Context().Done() // hold the connection open, silent
							break
						}
						if n > 0 {
							if _, werr := dst.Write(buf[:n]); werr != nil {
								break
							}
						}
						if err != nil {
							break
						}
					}
					done <- struct{}{}
				}
				go pipe(upstream, client)
				go pipe(client, upstream)
				<-done
			}()
		}
	}()
	return p
}

func (p *stallProxy) isStalled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stalled
}

func (p *stallProxy) stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stalled = true
}

// observers returns a probe of how many of the daemon at addr's
// connections observe live traffic, as its STATUS says: one per follow the
// gateway holds there.
func observers(t *testing.T, addr string) func() int {
	t.Helper()
	client, err := irc.Dial(irc.Config{Nick: "status-probe", Addr: addr, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return func() int {
		t.Helper()
		if err := client.Raw("STATUS"); err != nil {
			t.Fatal(err)
		}
		timeout := time.After(5 * time.Second)
		for {
			select {
			case event, ok := <-client.Events():
				if !ok {
					t.Fatal("the status connection closed")
				}
				if raw, ok := event.(*irc.RawEvent); ok && raw.Command == "770" {
					var status protocol.ServerStatus
					if err := json.Unmarshal([]byte(raw.Trailing), &status); err != nil {
						t.Fatal(err)
					}
					return status.Observers
				}
			case <-timeout:
				t.Fatal("no STATUS answer")
			}
		}
	}
}

// A hedged follow keeps delivering, each message once and in order, when
// the link it preferred stalls silently, and does not wait out the
// stalled link's idle time to do it.
func TestFollowHedgedRidesOutAStalledLink(t *testing.T) {
	addr := testserver.Start(t, server.Config{HistoryLimit: 1000}, nil)
	endpoints := followGateway(t, addr, 2, 1)
	proxy := newStallProxy(t, "127.0.0.1:"+strconv.Itoa(endpoints["framed"].Port))
	stalling := fallback.Endpoint{Carrier: protocol.Carrier{Name: "framed", Port: proxy.listener.Addr().(*net.TCPAddr).Port}}
	other := newStallProxy(t, "127.0.0.1:"+strconv.Itoa(endpoints["websocket"].Port)) // never stalled
	healthy := fallback.Endpoint{Carrier: protocol.Carrier{Name: "websocket", Port: other.listener.Addr().(*net.TCPAddr).Port}}
	followers := observers(t, addr)
	poster, err := irc.Dial(irc.Config{Nick: "hedge-poster", Addr: addr, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer poster.Close()

	c := &fallback.Client{Host: "127.0.0.1", Nick: "hedger", Endpoints: []fallback.Endpoint{stalling, healthy}}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	var mu sync.Mutex
	var got []string
	arrived := map[string]time.Time{}
	done := make(chan error, 1)
	go func() {
		done <- c.FollowHedged(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#hedge"}}, Limit: 50}, 20*time.Second, func(checked fallback.Checked) error {
			mu.Lock()
			defer mu.Unlock()
			for _, m := range checked.Rooms[0].Messages {
				got = append(got, m.Message)
				arrived[m.Message] = time.Now()
			}
			return nil
		})
	}()
	testwait.Every(t, 10*time.Second, 10*time.Millisecond, "both legs following", func() bool { return followers() == 2 })
	var sent []string
	post := func(n int) {
		for range n {
			body := fmt.Sprintf("hedged %d", len(sent))
			if err := poster.Send("#hedge", body); err != nil {
				t.Fatal(err)
			}
			sent = append(sent, body)
			time.Sleep(50 * time.Millisecond)
		}
	}
	post(10)
	proxy.stall()
	stalledAt := time.Now()
	post(20) // across lease renewals on the other link
	// A renewal is a new connection: the rest follows one more.
	renewals := other.accepted.Load()
	testwait.Until(t, 10*time.Second, "a lease renewal on the other link", func() bool { return other.accepted.Load() > renewals })
	post(10)
	deadline := time.Now().Add(10 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= len(sent) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(got) != fmt.Sprint(sent) {
		t.Fatalf("got %d messages %v, want %d in order, once each", len(got), got, len(sent))
	}
	// News after the stall came well within the stalled link's 20 s idle.
	if late := arrived[sent[len(sent)-1]].Sub(stalledAt); late > 10*time.Second {
		t.Fatalf("the last message came %v after the stall", late)
	}
}

// A hedged follow ends with emit's error, and with a failure once neither
// connection can follow; a client with Hedge set follows this way through
// a RoomReader.
func TestFollowHedgedEnds(t *testing.T) {
	addr := testserver.Start(t, server.Config{HistoryLimit: 100}, nil)
	endpoints := followGateway(t, addr, 30, 1)
	poster, err := irc.Dial(irc.Config{Nick: "hedge-ends", Addr: addr, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	defer poster.Close()
	c := &fallback.Client{Host: "127.0.0.1", Nick: "hedge-reader", Endpoints: []fallback.Endpoint{endpoints["framed"], endpoints["websocket"]}, Hedge: true}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	reader, err := c.ReadRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	followers := observers(t, addr)
	stop := errors.New("enough")
	emitted := 0
	followed := make(chan error, 1)
	go func() {
		followed <- reader.Follow(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#ends"}}, Limit: 10}, 5*time.Second, func(checked fallback.Checked) error {
			emitted += len(checked.Rooms[0].Messages)
			return stop
		})
	}()
	testwait.Every(t, 10*time.Second, 10*time.Millisecond, "both following", func() bool { return followers() == 2 })
	if err := poster.Send("#ends", "news"); err != nil {
		t.Fatal(err)
	}
	if err = <-followed; !errors.Is(err, stop) || emitted != 1 {
		t.Fatalf("emit's error: %v after %d messages", err, emitted)
	}

	// Nothing listens on either endpoint: the follow fails, it does not
	// wait forever.
	gone, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := gone.Addr().(*net.TCPAddr).Port
	_ = gone.Close()
	dead := &fallback.Client{Host: "127.0.0.1", Nick: "hedge-dead", Endpoints: []fallback.Endpoint{{Carrier: protocol.Carrier{Name: "framed", Port: port}}, {Carrier: protocol.Carrier{Name: "websocket", Port: port}}}}
	start := time.Now()
	if err := dead.FollowHedged(ctx, fallback.CheckSet{Rooms: []fallback.RoomAfter{{Room: "#ends"}}, Limit: 10}, 5*time.Second, func(fallback.Checked) error { return nil }); err == nil || time.Since(start) > 10*time.Second {
		t.Fatalf("unreachable gateway: %v after %v", err, time.Since(start))
	}
}
