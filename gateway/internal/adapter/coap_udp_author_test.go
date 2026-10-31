package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapnetclient "github.com/plgd-dev/go-coap/v3/net/client"
	"github.com/plgd-dev/go-coap/v3/options"
	coapudp "github.com/plgd-dev/go-coap/v3/udp"
	coapclient "github.com/plgd-dev/go-coap/v3/udp/client"
)

// An Observe watch over plain UDP CoAP runs once its ready marker is on the
// wire: the next notification is an AGD1 fragment of the watch's first frame.
// The fixture's AIRC is never reachable, so that frame is the watch's
// correlated failure; what matters is that the watch ran at all instead of
// waiting on a ready gate that only a write hook could open.
func TestServeCoAPUDPObserveRunsAfterReadyMarker(t *testing.T) {
	client, err := coapudp.Dial(serveCoAPUDPWatches(t, newRouteFixture(t)))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	exchange, done := context.WithTimeout(t.Context(), 5*time.Second)
	defer done()
	type note struct {
		sequence uint32
		format   message.MediaType
		body     []byte
	}
	notes := make(chan note, 8)
	observation := observeCoAPUDPWatch(t, exchange, client, func(n *pool.Message) {
		body, _ := n.ReadBody()
		sequence, _ := n.Observe()
		format, _ := n.ContentFormat()
		select {
		case notes <- note{sequence, format, append([]byte(nil), body...)}:
		default:
		}
	})
	defer func() { _ = observation.Cancel(context.Background()) }()
	next := func(what string) note {
		t.Helper()
		select {
		case n := <-notes:
			return n
		case <-time.After(5 * time.Second):
			t.Fatalf("no %s notification", what)
		}
		return note{}
	}
	if ready := next("ready"); ready.sequence != 0 || ready.format != message.AppJSON || !bytes.Contains(ready.body, []byte(`"event":"ready"`)) {
		t.Fatalf("ready marker %+v", ready)
	}
	first := next("first frame")
	_, _, _, count, payload, err := parseFragment(first.body)
	if first.sequence == 0 || first.format != message.AppOctets || err != nil || count != 1 {
		t.Fatalf("notification %d %v %q: %v", first.sequence, first.format, first.body, err)
	}
	var framed FrameResponse
	if err := json.Unmarshal(payload, &framed); err != nil || framed.CorrelationID != "watch" || framed.Sequence != 1 {
		t.Fatalf("notified frame %q: %v", payload, err)
	}
	// The peer still answers requests.
	answer, err := client.Get(exchange, "/v1/coap/rpc")
	if err != nil || answer.Code() != codes.MethodNotAllowed {
		t.Fatal("peer unusable after the watch:", err)
	}
	client.ReleaseMessage(answer)
}

// A Reset of a confirmable notification cancels its watch (RFC 7641 section
// 3.6). The client drops the watch's first
// page unacknowledged and answers it with a Reset carrying its message ID. The
// scripted AIRC would keep the watch alive, a page every StreamPageInterval,
// until its lease ends; instead its session ends at once and no later page
// follows. A shorter interval keeps the wait for that page brief.
func TestServeCoAPUDPResetCancelsWatch(t *testing.T) {
	interval := backend.StreamPageInterval
	t.Cleanup(func() { backend.StreamPageInterval = interval })
	backend.StreamPageInterval = 500 * time.Millisecond
	f := newRouteFixture(t)
	upstream := scriptCoAPUDPWatchUpstream(t, f.c.Upstream.Address)
	pages := make(chan int32, 16)
	// Blockwise would rewrite the outgoing Reset as a fresh request, so it is
	// off here and the Reset keeps the captured message ID on the wire.
	client, err := coapudp.Dial(serveCoAPUDPWatches(t, f), options.WithBlockwise(false, blockwise.SZX1024, 5*time.Second), coapUDPClientMonitor(func(_ *coapclient.Conn, m *pool.Message) (bool, error) {
		if format, _ := m.ContentFormat(); m.Code() != codes.Content || format != message.AppOctets {
			return false, nil
		}
		select {
		case pages <- m.MessageID():
		default:
		}
		return true, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	exchange, done := context.WithTimeout(t.Context(), 10*time.Second)
	defer done()
	observation := observeCoAPUDPWatch(t, exchange, client, func(*pool.Message) {})
	defer func() { _ = observation.Cancel(context.Background()) }()
	var mid int32
	select {
	case mid = <-pages:
	case <-exchange.Done():
		t.Fatal("no page notification")
	}
	paged := time.Now()
	select {
	case <-upstream:
		t.Fatal("the watch's AIRC session ended before the Reset")
	default:
	}
	reset := client.AcquireMessage(exchange)
	reset.SetType(message.Reset)
	reset.SetCode(codes.Empty)
	reset.SetMessageID(mid)
	err = client.WriteMessage(reset)
	client.ReleaseMessage(reset)
	if err != nil {
		t.Fatal("Reset write:", err)
	}
	resetAt := time.Now()
	select {
	case <-upstream:
	case <-time.After(2 * time.Second):
		t.Fatal("the Reset did not end the watch's AIRC session promptly")
	}
	t.Logf("watch ended %s after the Reset", time.Since(resetAt))
	// The next page was due one interval after the first.
	time.Sleep(time.Until(paged.Add(backend.StreamPageInterval * 5 / 4)))
	for len(pages) > 0 {
		if later := <-pages; later != mid {
			t.Fatalf("page MID=%d notified after the Reset of MID=%d", later, mid)
		}
	}
	// The peer still answers requests.
	answer, err := client.Get(exchange, "/v1/coap/rpc")
	if err != nil || answer.Code() != codes.MethodNotAllowed {
		t.Fatal("peer unusable after the Reset:", err)
	}
	client.ReleaseMessage(answer)
}

// serveCoAPUDPWatches serves f's coap_udp listener for watches of #fixture
// on a loopback port and returns its address.
func serveCoAPUDPWatches(t *testing.T, f *routeFixture) string {
	t.Helper()
	d := f.listener(t, config.Listener{ID: "coap-udp", Protocol: "coap_udp", Targets: []core.Target{{Kind: "room", Name: "#fixture"}}})
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- ServeCoAPUDP(ctx, conn, d, make(chan struct{}, 4), make(chan struct{}, 8)) }()
	t.Cleanup(func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	})
	return conn.LocalAddr().String()
}

// observeCoAPUDPWatch registers a guest watch of #fixture under correlation
// ID "watch" and observes it, passing each notification to notify.
func observeCoAPUDPWatch(t *testing.T, ctx context.Context, client *coapclient.Conn, notify func(*pool.Message)) coapnetclient.Observation {
	t.Helper()
	request := []byte(`{"version":1,"request_id":"udp-observe","operation":"watch","target":{"kind":"room","name":"#fixture"},"start":"retained","limit":8,"author":{"nick":"` + coapUDPWatcher + `"}}`)
	frame := []byte(`{"version":1,"correlation_id":"watch","profile":"plain","stream":true,"request":` + string(request) + `}`)
	response, err := client.Post(ctx, "/v1/coap/observe/register", message.AppJSON, bytes.NewReader(frame))
	if err != nil {
		t.Fatal(err)
	}
	body, err := response.ReadBody()
	client.ReleaseMessage(response)
	var registration CoAPRegistration
	if err != nil || json.Unmarshal(body, &registration) != nil || registration.CorrelationID != "watch" {
		t.Fatalf("registration %q: %v", body, err)
	}
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	observe, err := client.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observe.SetToken(token)
	observe.SetObserve(0)
	observation, err := client.DoObserve(observe, notify)
	client.ReleaseMessage(observe)
	if err != nil {
		t.Fatal(err)
	}
	return observation
}

const coapUDPWatcher = "udp-watcher"

// scriptCoAPUDPWatchUpstream serves the fixture's AIRC for one guest watch:
// it confirms the subscription and answers an empty retained page, then stays
// quiet. The returned channel closes when the gateway ends that session.
func scriptCoAPUDPWatchUpstream(t *testing.T, address string) <-chan struct{} {
	t.Helper()
	listener, err := net.Listen("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	var mu sync.Mutex
	var session net.Conn
	stopped := false
	go func() {
		defer close(ended)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		mu.Lock()
		session = conn
		if stopped {
			mu.Unlock()
			return
		}
		mu.Unlock()
		lines := bufio.NewScanner(conn)
		for lines.Scan() {
			line := lines.Text()
			switch {
			case strings.HasPrefix(line, "USER "):
				_, _ = fmt.Fprintf(conn, ":server 766 %[1]s :ephemeral\r\n:server 005 %[1]s CHECK=1 :features\r\n:server 001 %[1]s :welcome\r\n", coapUDPWatcher)
			case strings.HasPrefix(line, "OBSERVE "):
				_, _ = fmt.Fprintf(conn, ":server 765 %s %s\r\n", coapUDPWatcher, strings.TrimPrefix(line, "OBSERVE "))
			case strings.HasPrefix(line, "CHECK "):
				_, _ = fmt.Fprintf(conn, ":server 784 %[1]s :{\"kind\":\"page\",\"target\":\"#fixture\",\"status\":\"ok\"}\r\n:server 785 %[1]s :end\r\n", coapUDPWatcher)
			case strings.HasPrefix(line, "QUIT"):
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		stopped = true
		if session != nil {
			_ = session.Close()
		}
		mu.Unlock()
		<-ended
	})
	return ended
}

// coapUDPClientMonitor sees each message a go-coap UDP client receives before
// the client handles it; returning true drops it.
type coapUDPClientMonitor func(*coapclient.Conn, *pool.Message) (bool, error)

func (m coapUDPClientMonitor) UDPClientApply(c *coapclient.Config) { c.RequestMonitor = m }
