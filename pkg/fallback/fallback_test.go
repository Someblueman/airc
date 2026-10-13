package fallback

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testserver"
	"github.com/Someblueman/airc/pkg/irc"
)

// fakeGateway answers POST /v1/request like a gateway HTTP carrier. A send is
// queued, and its receipt is accepted on the next lookup; refuse and stall
// override that.
type fakeGateway struct {
	mu       sync.Mutex
	requests []request
	refuse   *failure      // answer every request with this error
	stall    time.Duration // hold every answer this long
	srv      *httptest.Server
}

func newFakeGateway(t *testing.T) *fakeGateway { return startFakeGateway(t, httptest.NewServer) }

// newFakeTLSGateway is a fake gateway behind TLS; its certificate is for
// example.com and 127.0.0.1.
func newFakeTLSGateway(t *testing.T) *fakeGateway { return startFakeGateway(t, httptest.NewTLSServer) }

func startFakeGateway(t *testing.T, start func(http.Handler) *httptest.Server) *fakeGateway {
	t.Helper()
	g := &fakeGateway{}
	g.srv = start(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req request
		if r.URL.Path != "/v1/request" || json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		g.mu.Lock()
		g.requests = append(g.requests, req)
		refuse, stall := g.refuse, g.stall
		g.mu.Unlock()
		if stall > 0 {
			select {
			case <-time.After(stall):
			case <-r.Context().Done():
				return
			}
		}
		if refuse != nil {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{"version": 1, "error": refuse})
			return
		}
		d := detached{Operation: req.Operation}
		switch req.Operation {
		case "send", "reply":
			d.Result = &result{RequestID: req.RequestID, State: "queued"}
		case "receipt.get":
			d.Result = &result{RequestID: req.LookupID, State: "accepted", MessageID: strings.Repeat("a", 32)}
		case "history.read":
			d.Page = &page{Messages: []*irc.MessageMetadata{{ID: strings.Repeat("b", 32), From: "bob", Target: req.Target.Name, Message: "hi", Timestamp: time.Unix(1, 0)}}, Status: "ok"}
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(d)
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGateway) endpoint(t *testing.T) Endpoint {
	t.Helper()
	_, port, _ := net.SplitHostPort(g.srv.Listener.Addr().String())
	name := "http:"
	if g.srv.TLS != nil {
		name = "http+tls:"
	}
	e, err := ParseEndpoint(name + port)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func (g *fakeGateway) seen() []request {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]request(nil), g.requests...)
}

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func ircEndpoint(port int) Endpoint {
	return Endpoint{Carrier: protocol.Carrier{Name: "irc", Port: port}}
}

func TestParseEndpoints(t *testing.T) {
	list, err := ParseEndpoints("irc:6667, http+tls:gw.example:6680,websocket:[::1]:6681,unix:/run/airc/gateway.sock")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"irc:6667", "http+tls:gw.example:6680", "websocket:[::1]:6681", "unix:/run/airc/gateway.sock"}
	if len(list) != len(want) {
		t.Fatal(list)
	}
	for i, e := range list {
		if e.String() != want[i] {
			t.Errorf("entry %d: %q, want %q", i, e, want[i])
		}
	}
	if list[1].Host != "gw.example" || !list[1].TLS || list[1].Port != 6680 || list[2].Host != "::1" {
		t.Fatalf("%+v", list)
	}
	for _, bad := range []string{"http", "http:0", "HTTP:80", "http:host:", "http:a b:80", "http+tls:/tmp/x.sock"} {
		if _, err := ParseEndpoints(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if list, err := ParseEndpoints(" "); err != nil || list != nil {
		t.Fatal(list, err)
	}
}

// Native IRC first; when it is unreachable, then the next carrier, then the
// next, all with one request ID: the gateway's journal and AIRC both key it
// by author, so the attempts are one message.
func TestPostFallsBackInOrderWithOneRequestID(t *testing.T) {
	down, up := newFakeGateway(t), newFakeGateway(t)
	down.refuse = &failure{Code: "upstream_unavailable", Transient: true}
	var tried []string
	c := &Client{Host: "127.0.0.1", Nick: "alice", Endpoints: []Endpoint{ircEndpoint(closedPort(t)), down.endpoint(t), up.endpoint(t)},
		Attempted: func(e Endpoint, err error) { tried = append(tried, e.Name+":"+strconv.FormatBool(err == nil)) }}
	receipt, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.MessageID != strings.Repeat("a", 32) || receipt.Endpoint != up.endpoint(t) || receipt.RequestID != "req-1" {
		t.Fatalf("%+v", receipt)
	}
	if strings.Join(tried, ",") != "irc:false,http:false,http:true" {
		t.Fatal(tried)
	}
	for _, g := range []*fakeGateway{down, up} {
		seen := g.seen()
		if len(seen) == 0 || seen[0].RequestID != "req-1" || seen[0].Operation != "send" || seen[0].Author == nil || seen[0].Author.Nick != "alice" || seen[0].Author.Token != "" || seen[0].Target.Kind != "room" {
			t.Fatalf("%+v", seen)
		}
	}
	if seen := up.seen(); len(seen) != 2 || seen[1].Operation != "receipt.get" || seen[1].LookupID != "req-1" {
		t.Fatalf("receipt was not followed: %+v", seen)
	}
}

// A definite refusal is the answer: no later carrier is tried.
func TestPostStopsAtDefiniteRefusal(t *testing.T) {
	for _, code := range []string{"nick_registered", "authentication_failed", "forbidden", "invalid_request"} {
		refusing, next := newFakeGateway(t), newFakeGateway(t)
		refusing.refuse = &failure{Code: code}
		c := &Client{Host: "127.0.0.1", Nick: "alice", Token: strings.Repeat("f", 64), Endpoints: []Endpoint{refusing.endpoint(t), next.endpoint(t)}}
		_, err := c.Post(t.Context(), Message{Target: "bob", Body: "hello", RequestID: "req-2"})
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != code || !Definite(err) || Unreachable(err) || MayHavePosted(err) {
			t.Fatalf("%s: %v", code, err)
		}
		if len(next.seen()) != 0 {
			t.Fatalf("%s: tried another carrier after a refusal", code)
		}
		if seen := refusing.seen(); seen[0].Author.Token != c.Token || seen[0].Target.Kind != "direct" {
			t.Fatalf("%+v", seen[0])
		}
		if strings.Contains(err.Error(), c.Token) {
			t.Fatal("error exposes the token")
		}
	}
}

// An attempt that stops answering may have posted; the next carrier's retry
// with the same request ID resolves it. When none answers, the caller learns
// the outcome is unknown.
func TestUncertainAttemptIsResolvedByTheNextCarrier(t *testing.T) {
	stalled, next := newFakeGateway(t), newFakeGateway(t)
	stalled.stall = 5 * time.Second
	c := &Client{Host: "127.0.0.1", Nick: "alice", CallTimeout: 200 * time.Millisecond, Endpoints: []Endpoint{stalled.endpoint(t), next.endpoint(t)}}
	receipt, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "req-3"})
	if err != nil || receipt.Endpoint != next.endpoint(t) {
		t.Fatal(receipt, err)
	}
	if s, n := stalled.seen(), next.seen(); s[0].RequestID != "req-3" || n[0].RequestID != "req-3" {
		t.Fatal(s, n)
	}
	c.Endpoints = c.Endpoints[:1]
	_, err = c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "req-4"})
	var all *Unavailable
	if !errors.As(err, &all) || !all.MayHavePosted || !MayHavePosted(err) || !Unreachable(err) || Definite(err) {
		t.Fatalf("%v", err)
	}
}

// Carriers that refuse the connection cannot have posted: the caller may
// forget the request instead of treating its outcome as unknown.
func TestUnreachableCarriersDidNotPost(t *testing.T) {
	c := &Client{Host: "127.0.0.1", Nick: "alice"}
	for _, name := range []string{"irc", "http", "websocket", "framed"} {
		c.Endpoints = append(c.Endpoints, Endpoint{Carrier: protocol.Carrier{Name: name, Port: closedPort(t)}})
	}
	_, err := c.Post(t.Context(), Message{Target: "#room", Body: "hello", RequestID: "req-5"})
	var all *Unavailable
	if !errors.As(err, &all) || len(all.Attempts) != 4 || all.MayHavePosted || MayHavePosted(err) || !Unreachable(err) {
		t.Fatalf("%v", err)
	}
}

func TestUnusableEndpointsAreSkipped(t *testing.T) {
	g := newFakeGateway(t)
	remote := []Endpoint{
		{Carrier: protocol.Carrier{Name: "mqtt", Port: 1883}},
		{Carrier: protocol.Carrier{Name: "http", Port: 6680}},                // plaintext to a remote host
		{Carrier: protocol.Carrier{Name: "unix", Path: "/tmp/gateway.sock"}}, // another machine's socket
	}
	// A registered author's token never crosses a remote plaintext link.
	c := &Client{Host: "chat.example", Nick: "alice", Token: strings.Repeat("a", 64), Endpoints: remote}
	_, err := c.History(t.Context(), "#room", 10)
	var all *Unavailable
	if !errors.As(err, &all) || len(all.Attempts) != 3 || all.MayHavePosted {
		t.Fatal(err)
	}
	// An endpoint naming its own loopback host is usable from a remote Host.
	local := g.endpoint(t)
	local.Host = "127.0.0.1"
	c.Endpoints = append(remote, local)
	page, err := c.History(t.Context(), "#room", 10)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Message != "hi" {
		t.Fatal(page, err)
	}
	if seen := g.seen(); seen[0].Operation != "history.read" || seen[0].Start != "recent" || seen[0].Limit != 10 {
		t.Fatalf("%+v", seen)
	}
}

func TestRemotePlaintextOnlyForGuestsWithoutCredentials(t *testing.T) {
	plain := Endpoint{Carrier: protocol.Carrier{Name: "http", Port: 6680}}
	secure := Endpoint{Carrier: protocol.Carrier{Name: "http", TLS: true, Port: 6680}}
	for _, c := range []struct {
		client Client
		ok     bool
	}{
		{Client{Host: "chat.example", Nick: "guest"}, true},
		{Client{Host: "chat.example", Nick: "alice", Token: strings.Repeat("a", 64)}, false},
		{Client{Host: "chat.example", Nick: "guest", Password: strings.Repeat("b", 64)}, false},
	} {
		if err := c.client.usable(plain); (err == nil) != c.ok {
			t.Errorf("token=%t password=%t: %v", c.client.Token != "", c.client.Password != "", err)
		}
		if err := c.client.usable(secure); err != nil {
			t.Errorf("TLS carrier refused: %v", err)
		}
	}
}

func TestUnreachableClassification(t *testing.T) {
	_, dialErr := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(closedPort(t)))
	for _, err := range []error{dialErr, context.DeadlineExceeded, errors.Join(errors.New("register"), errors.New("x"), net.ErrClosed)} {
		if !Unreachable(err) {
			t.Errorf("not unreachable: %v", err)
		}
	}
	for _, err := range []error{errors.New("invalid identity file"), &irc.RejectedError{Code: "498"}, &irc.NickInUseError{Nick: "a"}, &Refusal{Code: "forbidden"}} {
		if Unreachable(err) {
			t.Errorf("unreachable: %v", err)
		}
	}
}

// The native carrier is AIRC itself: a post there carries the request ID as
// its retry key, so a later gateway retry of the same ID is the same message.
func TestNativeCarrierPostsReadsAndDiscovers(t *testing.T) {
	advertised := []protocol.Carrier{{Name: "irc", Port: 6667}, {Name: "http", Port: 6680}}
	addr := testserver.Start(t, server.Config{HistoryLimit: 100, Carriers: advertised}, nil)
	_, port, _ := net.SplitHostPort(addr)
	p, _ := strconv.Atoi(port)
	c := &Client{Host: "127.0.0.1", Nick: "alice", Endpoints: []Endpoint{ircEndpoint(p)}}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	first, err := c.Post(ctx, Message{Target: "#room", Body: "hello", RequestID: "native-1"})
	if err != nil || !protocol.ValidMessageID(first.MessageID) || first.Persisted == nil {
		t.Fatal(first, err)
	}
	again, err := c.Post(ctx, Message{Target: "#room", Body: "hello", RequestID: "native-1"})
	if err != nil || again.MessageID != first.MessageID {
		t.Fatal("retry posted again", again, err)
	}
	if _, err := c.Post(ctx, Message{Target: "#room", Body: "changed", RequestID: "native-1"}); !Definite(err) {
		t.Fatal("conflicting retry was not refused", err)
	}
	found, err := c.Lookup(ctx, "native-1")
	if err != nil || found.MessageID != first.MessageID {
		t.Fatal(found, err)
	}
	page, err := c.History(ctx, "#room", 10)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != first.MessageID {
		t.Fatal(page, err)
	}
	carriers, from, err := c.Carriers(ctx)
	if err != nil || protocol.FormatCarriers(carriers) != "irc:6667,http:6680" || from.Name != "irc" {
		t.Fatal(carriers, from, err)
	}
}
