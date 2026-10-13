package fallback

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/coder/websocket"
)

// silentListener accepts connections and never answers, like a port whose
// service hung or a middlebox that swallows the traffic.
func silentListener(t *testing.T) (net.Listener, *atomic.Int32) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	t.Cleanup(func() {
		_ = l.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return l, &accepted
}

func portOf(t *testing.T, address string) int {
	t.Helper()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(port)
	return p
}

// fakeSocketGateway answers the WebSocket carrier, optionally holding each
// handshake for delay first; frames counts the requests it received.
type fakeSocketGateway struct {
	srv    *httptest.Server
	delay  time.Duration
	frames atomic.Int32
}

func newFakeSocketGateway(t *testing.T, delay time.Duration) *fakeSocketGateway {
	t.Helper()
	g := &fakeSocketGateway{delay: delay}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(g.delay):
		case <-r.Context().Done():
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"airc-gateway.v1"}})
		if err != nil {
			return
		}
		defer conn.CloseNow()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			g.frames.Add(1)
			var f frame
			if json.Unmarshal(data, &f) != nil || f.Request == nil {
				return
			}
			d := detached{Operation: f.Request.Operation, Result: &result{RequestID: f.Request.RequestID, State: "accepted", MessageID: strings.Repeat("c", 32)}}
			answer, _ := json.Marshal(frameAnswer{CorrelationID: f.CorrelationID, Event: "result", Result: &d})
			if conn.Write(r.Context(), websocket.MessageText, answer) != nil {
				return
			}
		}
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeSocketGateway) endpoint(t *testing.T) Endpoint {
	return Endpoint{Carrier: protocol.Carrier{Name: "websocket", Port: portOf(t, g.srv.Listener.Addr().String())}}
}

// A native port that accepts but never registers costs the stagger, not the
// connect timeout: the next carrier is dialled once the first goes quiet.
func TestSilentNativePortDelaysTheNextCarrierOnlyByTheStagger(t *testing.T) {
	silent, accepted := silentListener(t)
	g := newFakeGateway(t)
	var tried []string
	c := &Client{Host: "127.0.0.1", Nick: "alice", Stagger: 100 * time.Millisecond, ConnectTimeout: 5 * time.Second,
		Endpoints: []Endpoint{ircEndpoint(portOf(t, silent.Addr().String())), g.endpoint(t)},
		Attempted: func(e Endpoint, err error) { tried = append(tried, e.Name+":"+strconv.FormatBool(err == nil)) }}
	started := time.Now()
	receipt, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-1"})
	elapsed := time.Since(started)
	if err != nil || receipt.Endpoint != g.endpoint(t) {
		t.Fatal(receipt, err)
	}
	if elapsed > time.Second {
		t.Fatalf("failover took %s with a 100ms stagger", elapsed)
	}
	if accepted.Load() != 1 {
		t.Fatalf("native port saw %d connections", accepted.Load())
	}
	// The abandoned native connection is not an attempt.
	if strings.Join(tried, ",") != "http:true" {
		t.Fatal(tried)
	}
}

// A TLS carrier whose handshake never completes is passed over the same way.
func TestSilentTLSHandshakeIsPassedOver(t *testing.T) {
	silent, _ := silentListener(t)
	g := newFakeGateway(t)
	quiet := Endpoint{Carrier: protocol.Carrier{Name: "framed", TLS: true, Port: portOf(t, silent.Addr().String())}}
	c := &Client{Host: "127.0.0.1", Nick: "alice", Stagger: 100 * time.Millisecond, Endpoints: []Endpoint{quiet, g.endpoint(t)}}
	started := time.Now()
	if _, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-2"}); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("failover took %s", elapsed)
	}
}

// The operation runs on the winner only: a carrier that was still
// connecting when another won is closed and never sees the request.
func TestOnlyTheWinnerReceivesTheRequest(t *testing.T) {
	slow := newFakeSocketGateway(t, 400*time.Millisecond)
	fast := newFakeGateway(t)
	unused := newFakeGateway(t)
	c := &Client{Host: "127.0.0.1", Nick: "alice", Stagger: 50 * time.Millisecond,
		Endpoints: []Endpoint{slow.endpoint(t), fast.endpoint(t), unused.endpoint(t)}}
	receipt, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-3"})
	if err != nil || receipt.Endpoint != fast.endpoint(t) {
		t.Fatal(receipt, err)
	}
	time.Sleep(600 * time.Millisecond) // past the slow handshake
	if n := slow.frames.Load(); n != 0 {
		t.Fatalf("the losing carrier received %d requests", n)
	}
	if n := len(unused.seen()); n != 0 {
		t.Fatalf("a carrier never needed received %d requests", n)
	}
	if seen := fast.seen(); len(seen) != 2 || seen[0].Operation != "send" {
		t.Fatalf("%+v", seen)
	}
	// Given time, the slow carrier wins on its own and answers.
	c.Endpoints = []Endpoint{slow.endpoint(t), unused.endpoint(t)}
	c.Stagger = 2 * time.Second
	receipt, err = c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-4"})
	if err != nil || receipt.Endpoint != slow.endpoint(t) || slow.frames.Load() != 1 || len(unused.seen()) != 0 {
		t.Fatal(receipt, err, slow.frames.Load())
	}
}

// A healthy native server registers before the stagger: no other carrier is
// dialled, so the happy path is unchanged.
func TestHealthyNativeWinsWithoutDiallingOthers(t *testing.T) {
	addr := testserver.Start(t, server.Config{HistoryLimit: 10}, nil)
	silent, accepted := silentListener(t)
	gateway := Endpoint{Carrier: protocol.Carrier{Name: "http", Port: portOf(t, silent.Addr().String())}}
	c := &Client{Host: "127.0.0.1", Nick: "alice", Stagger: 2 * time.Second, Endpoints: []Endpoint{ircEndpoint(portOf(t, addr)), gateway}}
	k, err := c.Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	client := k.Native()
	if client == nil || k.Endpoint.Name != "irc" {
		t.Fatalf("winner %s", k.Endpoint)
	}
	defer client.Close()
	if accepted.Load() != 0 {
		t.Fatal("dialled a gateway carrier while native IRC was registering")
	}
}

// A definite refusal while connecting (here native IRC refusing a guest on a
// registered nickname) ends the race; no other carrier is tried.
func TestRefusalWhileConnectingEndsTheRace(t *testing.T) {
	addr := testserver.Start(t, server.Config{}, func(s *server.Server) error { return s.RestoreAccounts(filepath.Join(t.TempDir(), "accounts")) })
	owner, err := irc.DialContext(t.Context(), irc.Config{Nick: "carol", Addr: addr, IdentityToken: strings.Repeat("a", 64), CreateAccount: true, Ephemeral: true})
	if err != nil {
		t.Fatal(err)
	}
	_ = owner.Close()
	g := newFakeGateway(t)
	c := &Client{Host: "127.0.0.1", Nick: "carol", Stagger: 2 * time.Second, Endpoints: []Endpoint{ircEndpoint(portOf(t, addr)), g.endpoint(t)}}
	_, err = c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-5"})
	var refusal *Refusal
	if !errors.As(err, &refusal) || refusal.Endpoint.Name != "irc" {
		t.Fatal(err)
	}
	if len(g.seen()) != 0 {
		t.Fatal("tried a carrier after a definite refusal")
	}
}

// The caller's deadline bounds the race even when every endpoint is silent.
func TestRaceRespectsTheCallersDeadline(t *testing.T) {
	var endpoints []Endpoint
	for range 3 {
		silent, _ := silentListener(t)
		endpoints = append(endpoints, ircEndpoint(portOf(t, silent.Addr().String())))
	}
	c := &Client{Host: "127.0.0.1", Nick: "alice", Stagger: 50 * time.Millisecond, ConnectTimeout: 10 * time.Second, Endpoints: endpoints}
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := c.Post(ctx, Message{Target: "#room", Body: "hello", RequestID: "race-6"})
	var all *Unavailable
	if !errors.As(err, &all) || len(all.Attempts) != 3 || all.MayHavePosted {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("returned after %s", elapsed)
	}
}

// An operation that fails after its carrier won goes on to the carriers the
// race left untried, with the same request ID.
func TestFailureAfterWinningContinuesWithTheRest(t *testing.T) {
	stalled, next := newFakeGateway(t), newFakeGateway(t)
	stalled.stall = 5 * time.Second
	c := &Client{Host: "127.0.0.1", Nick: "alice", CallTimeout: 200 * time.Millisecond, Endpoints: []Endpoint{stalled.endpoint(t), next.endpoint(t)}}
	k, err := c.Connect(t.Context())
	if err != nil || k.Endpoint != stalled.endpoint(t) {
		t.Fatal(k, err)
	}
	receipt, err := k.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "race-7"})
	if err != nil || receipt.Endpoint != next.endpoint(t) {
		t.Fatal(receipt, err)
	}
	if s, n := stalled.seen(), next.seen(); len(s) != 1 || s[0].RequestID != "race-7" || n[0].RequestID != "race-7" {
		t.Fatal(s, n)
	}
}
