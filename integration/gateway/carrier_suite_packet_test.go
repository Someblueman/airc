//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

// A network datagram carrier (UDP) takes
// each plain correlated frame as AGD1 fragments and answers the request's
// source with fragments of the same kind. There is no connection, so there
// is no identify and a watch cannot be cancelled; a frame it cannot verify
// gets no answer at all.
var _ = registerSuiteCarriers(
	packetSuiteCarrier("udp", config.Listener{Protocol: "udp"}, packetCarrier{
		configure: func(t *testing.T, l *config.Listener) {
			l.Address = net.JoinHostPort("127.0.0.1", fmt.Sprint(reserveUDPPort(t, "udp4", net.IPv4(127, 0, 0, 1))))
		},
		socket: func(*testing.T) (*net.UDPConn, error) {
			return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		},
		bind: bindUDPPort("udp4", net.IPv4zero),
	}),
)

// packetCarrier is how the suite reaches one network datagram carrier.
type packetCarrier struct {
	// configure gives each listener its own address.
	configure func(t *testing.T, l *config.Listener)
	// socket opens a client socket, whose address the gateway answers.
	socket func(t *testing.T) (*net.UDPConn, error)
	// bind binds a stopped listener's port again.
	bind func(port int) (io.Closer, error)
	// wire, if set, runs the carrier's own checks beyond the packet ones.
	wire func(t *testing.T, e *suiteEnv, p packetCarrier)
}

func packetSuiteCarrier(name string, listener config.Listener, p packetCarrier) suiteCarrier {
	return suiteCarrier{
		name: name, listener: listener, configure: p.configure, results: true, reads: true,
		dial: func(t *testing.T, d suiteDial) (suiteClient, error) { return dialPacketSuite(t, p, d.address) },
		wire: func(t *testing.T, e *suiteEnv) { packetSuiteWire(t, e, p) },
	}
}

// packetSuiteClient sends plain frames from its own socket to the listener.
type packetSuiteClient struct {
	conn        *net.UDPConn
	destination *net.UDPAddr
	sequence    int
}

func dialPacketSuite(t *testing.T, p packetCarrier, address string) (*packetSuiteClient, error) {
	destination, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		return nil, err
	}
	conn, err := p.socket(t)
	if err != nil {
		return nil, err
	}
	return &packetSuiteClient{conn: conn, destination: destination}, nil
}

// packetSilence is how long a client waits before taking silence as the
// answer: longer than the gateway's own four-second request deadline.
const packetSilence = 6 * time.Second

// packetFrame is one plain correlated frame; the request is embedded
// verbatim, so it may be invalid JSON.
func packetFrame(id string, request []byte, stream bool) []byte {
	return fmt.Appendf(nil, `{"version":1,"correlation_id":%q,"profile":"plain","stream":%t,"request":%s}`, id, stream, request)
}

// agd1Fragments splits a frame into AGD1 datagrams under a fresh frame ID.
func agd1Fragments(t *testing.T, frame []byte) [][]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return webTransportDatagrams(frame, id)
}

func (c *packetSuiteClient) writeDatagrams(t *testing.T, datagrams ...[]byte) {
	t.Helper()
	_ = c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	for _, datagram := range datagrams {
		if _, err := c.conn.WriteToUDP(datagram, c.destination); err != nil {
			t.Fatal(err)
		}
	}
}

func (c *packetSuiteClient) write(t *testing.T, id string, request []byte, stream bool) {
	t.Helper()
	c.writeDatagrams(t, agd1Fragments(t, packetFrame(id, request, stream))...)
}

// read assembles the next AGD1 response, or returns nil when none arrives
// within wait.
func (c *packetSuiteClient) read(t *testing.T, wait time.Duration) *adapter.FrameResponse {
	t.Helper()
	pending := map[string]map[uint16][]byte{}
	_ = c.conn.SetReadDeadline(time.Now().Add(wait))
	for {
		buffer := make([]byte, 1001)
		n, err := c.conn.Read(buffer)
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() && len(pending) == 0 {
			return nil
		}
		if err != nil {
			t.Fatal("AGD1 response:", err)
		}
		fragment := buffer[:n]
		if n < webTransportDatagramHeader || string(fragment[:4]) != "AGD1" {
			t.Fatalf("not an AGD1 datagram: %q", fragment)
		}
		id, index, count := string(fragment[4:20]), binary.BigEndian.Uint16(fragment[24:26]), int(binary.BigEndian.Uint16(fragment[26:28]))
		if pending[id] == nil {
			pending[id] = map[uint16][]byte{}
		}
		pending[id][index] = fragment
		if len(pending[id]) == count {
			fragments := make([][]byte, 0, count)
			for _, f := range pending[id] {
				fragments = append(fragments, f)
			}
			var response adapter.FrameResponse
			if body := assembleResponseDatagrams(t, fragments); json.Unmarshal(body, &response) != nil || response.Version != 1 {
				t.Fatalf("response frame %s", body)
			}
			return &response
		}
	}
}

func (c *packetSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.sequence++
	id := fmt.Sprint("call-", c.sequence)
	c.write(t, id, request, false)
	response := c.read(t, packetSilence)
	switch {
	case response == nil:
		return suiteReply{closed: true}
	case response.CorrelationID != id || response.Sequence != 1:
		t.Fatalf("uncorrelated reply %+v", response)
	case response.Event == "error" && response.Error != nil:
		return suiteReply{failure: response.Error}
	case response.Event != "result" || response.Result == nil:
		t.Fatalf("reply %+v", response)
	}
	return suiteReply{result: response.Result}
}

func (c *packetSuiteClient) send(t *testing.T, request []byte) {
	t.Helper()
	c.sequence++
	c.write(t, fmt.Sprint("send-", c.sequence), request, false)
}

func (c *packetSuiteClient) close() { _ = c.conn.Close() }

// silent checks that nothing more arrives for the client.
func (c *packetSuiteClient) silent(t *testing.T) {
	t.Helper()
	if response := c.read(t, 500*time.Millisecond); response != nil {
		t.Fatalf("unexpected reply %+v", response)
	}
}

// packetSuiteWire covers what datagrams add to the contract: the watch mode
// they report, input the gateway cannot verify going unanswered, reordered
// fragments, a watch's retained and live pages, a history larger than one
// reply, and a shutdown with a watch and a partial request outstanding.
func packetSuiteWire(t *testing.T, e *suiteEnv, p packetCarrier) {
	t.Run("capabilities", func(t *testing.T) {
		r := e.dial(t).call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("capabilities"), "operation": core.CapabilitiesGet}, e.guest("wire"), "")))
		if r.result == nil || r.result.Capabilities == nil {
			t.Fatalf("capabilities: %v", r)
		}
		caps := r.result.Capabilities
		if !reflect.DeepEqual(caps.WatchModes, []string{"long_poll", "udp_mailbox_datagrams"}) || caps.MaxRequestFrameBytes != adapter.MaxRequestFrameBytes || caps.MaxResponseFrameBytes != adapter.MaxResponseFrameBytes {
			t.Fatalf("capabilities %+v", caps)
		}
	})
	t.Run("unverified_input", func(t *testing.T) {
		// Nothing the gateway cannot verify is answered or reaches intake,
		// and the source is answered normally afterwards.
		client := e.dial(t).(*packetSuiteClient)
		for _, kind := range []string{"unknown_profile", "malformed", "override", "oversize", "incomplete"} {
			id := e.id(kind)
			request := sendBody(t, id, "#wire", "must not post "+strings.Repeat("p", 3500), e.guest("wire"), "")
			frame := packetFrame(id, request, false)
			switch kind {
			case "unknown_profile":
				frame = []byte(strings.Replace(string(frame), `"profile":"plain"`, `"profile":"unknown"`, 1))
			case "malformed":
				frame = packetFrame(id, []byte("[]"), false)
			case "override":
				frame = append(frame[:len(frame)-1], `,"destination":"127.0.0.1:9"}`...)
			}
			datagrams := agd1Fragments(t, frame)
			switch kind {
			case "oversize":
				datagrams = [][]byte{make([]byte, 1001)}
			case "incomplete":
				datagrams = datagrams[:1]
			}
			client.writeDatagrams(t, datagrams...)
			client.silent(t)
			e.noJob(t, id)
		}
		r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("after"), "operation": core.CapabilitiesGet}, e.guest("wire"), "")))
		if r.result == nil || r.result.Capabilities == nil {
			t.Fatalf("after unverified input: %v", r)
		}
		client.silent(t)
	})
	t.Run("shutdown_joins_watch", func(t *testing.T) {
		// A gateway of its own stops promptly with a watch streaming and a
		// request half received: it releases the port and aircd's read
		// lease, and the partial request never reaches intake.
		l := e.carrier.listener
		l.ID, l.Limits, l.Targets = "solo", fixtureListenerLimits(), []core.Target{{Kind: "room", Name: "#wire"}}
		p.configure(t, &l)
		c := authorConfig(t, e.socket, e.config.Upstream.GatewayTokenFile, l)
		g := startAuthorGateway(t, c)
		client, err := dialPacketSuite(t, p, g.addresses["solo"])
		if err != nil {
			t.Fatal(err)
		}
		defer client.close()
		baseline := nativeObserverCount(t, e.statusClient)
		client.write(t, "watch", watchBody(t, e.id("solo-watch"), "#wire", e.guest("watcher")), true)
		if page := client.read(t, packetSilence); page == nil || page.Event != "page" {
			t.Fatalf("watch did not start: %+v", page)
		}
		waitNativeObserverCount(t, e.statusClient, baseline+1)
		id := e.id("partial")
		datagrams := agd1Fragments(t, packetFrame(id, sendBody(t, id, "#wire", strings.Repeat("x", 3500), e.guest("wire"), ""), false))
		client.writeDatagrams(t, datagrams[0])
		started := time.Now()
		g.stop(t)
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatalf("shutdown took %v", elapsed)
		}
		bound, err := p.bind(client.destination.Port)
		if err != nil {
			t.Fatal("listener port retained after shutdown:", err)
		}
		_ = bound.Close()
		waitNativeObserverCountAtMost(t, e.statusClient, baseline)
		(&suiteEnv{config: c}).noJob(t, id)
	})
	t.Run("reordered_fragments", func(t *testing.T) {
		client := e.dial(t).(*packetSuiteClient)
		id, text := e.id("reordered"), e.text("reordered")+" "+strings.Repeat("λ", 1400)
		datagrams := agd1Fragments(t, packetFrame(id, sendBody(t, id, "#wire", text, e.guest("wire"), ""), false))
		if len(datagrams) < 2 {
			t.Fatal("request did not fragment")
		}
		for i := len(datagrams) - 1; i >= 0; i-- {
			client.writeDatagrams(t, datagrams[i])
		}
		r := client.read(t, packetSilence)
		if r == nil || r.Result == nil {
			t.Fatalf("reordered request: %+v", r)
		}
		e.admitted(t, suiteReply{result: r.Result}, id)
		e.posted(t, id, "#wire", text, e.guest("wire"), "")
	})
	t.Run("watch_pages", func(t *testing.T) {
		// A retained page equals aircd's history; a live page carries the
		// whole record aircd reports, less the sequence and receipt only
		// CHECK has.
		postNative(t, e.socket, e.guest("seed"), "#wire", "retained "+e.text("watch"))
		client := e.dial(t).(*packetSuiteClient)
		baseline := nativeObserverCount(t, e.statusClient)
		client.write(t, "watch", watchBody(t, e.id("watch"), "#wire", e.guest("watcher")), true)
		first := client.read(t, packetSilence)
		if first == nil || first.Event != "page" || first.Sequence != 1 || first.Page == nil || !reflect.DeepEqual(first.Page.Messages, nativeWebTransportMessages(t, e.socket, "#wire")) {
			t.Fatalf("retained page %+v", first)
		}
		waitNativeObserverCount(t, e.statusClient, baseline+1)
		nick, body := e.guest("live"), "live "+e.text("watch")
		postNative(t, e.socket, nick, "#wire", body)
		live := client.read(t, packetSilence)
		if live == nil || live.Event != "page" || live.Sequence != 2 || live.Page == nil || len(live.Page.Messages) != 1 {
			t.Fatalf("live page %+v", live)
		}
		native := nativeWebTransportMessages(t, e.socket, "#wire")
		got, want := live.Page.Messages[0], *native[len(native)-1]
		if got.Message != body || got.From != nick || got.ID == "" || got.Seq != 0 || got.Receipt != nil || want.Seq == 0 {
			t.Fatalf("live record %+v, aircd %+v", got, want)
		}
		want.Seq, want.Receipt = 0, nil
		if !reflect.DeepEqual(got, &want) {
			t.Fatalf("live record %+v, aircd %+v", got, want)
		}
	})
	t.Run("history_larger_than_a_reply", func(t *testing.T) {
		// Sixteen 4000-byte records exceed one reply: the answer is a
		// partial page that reports more, or a whole-page capacity error,
		// never a truncated record, and one-record pages then read every
		// record as aircd holds it.
		author := dial(t, e.socket, e.guest("bulk"), "", false, true)
		for i := range 16 {
			id := fmt.Sprintf("%s-bulk-%d", e.carrier.name, i)
			if err := author.SendWithID("#wire", strings.Repeat("x", 4000), id); err != nil {
				t.Fatal(err)
			}
			next(t, author, func(r *irc.SendReceiptEvent) bool { return r.RequestID == id })
		}
		native := nativeHistory(t, e.socket, "#wire", 64)
		client, reader := e.dial(t), e.guest("reader")
		read := func(id string, limit int, cursor string) suiteReply {
			fields := map[string]any{"request_id": id, "operation": core.HistoryRead, "target": map[string]any{"kind": "room", "name": "#wire"}, "limit": limit}
			if cursor == "" {
				fields["start"] = "retained"
			} else {
				fields["cursor"] = cursor
			}
			return client.call(t, mustBody(t, plainRequest(fields, reader, "")))
		}
		id := e.id("history")
		whole := read(id, 32, "")
		partial := whole.result != nil && whole.result.Page != nil && whole.result.Page.Status == "more" && len(whole.result.Page.Messages) > 0 && len(whole.result.Page.Messages) < len(native)
		if !partial && (whole.failure == nil || whole.failure.Code != core.CapacityExceeded) {
			t.Fatalf("oversized history: %v", whole)
		}
		e.noJob(t, id) // a read is never journaled
		cursor := ""
		for i := 0; i <= len(native); i++ {
			r := read(e.id("history"), 1, cursor)
			if r.result == nil || r.result.Page == nil {
				t.Fatalf("page %d: %v", i, r)
			}
			page, status, want := r.result.Page, "more", native[min(i, len(native)-1):min(i+1, len(native))]
			if i >= len(native)-1 {
				status = "ok"
			}
			if i == len(native) {
				want = nil
			}
			if page.Gap || page.Status != status || page.Cursor == "" || len(page.Messages) != len(want) || len(want) == 1 && !reflect.DeepEqual(page.Messages[0], want[0]) {
				t.Fatalf("page %d: %+v, aircd holds %+v", i, page, want)
			}
			cursor = page.Cursor
		}
	})
	if p.wire != nil {
		p.wire(t, e, p)
	}
}

// nativeHistory is up to limit of room's oldest records, as aircd's CHECK
// reports them.
func nativeHistory(t *testing.T, socket, room string, limit int) []*irc.MessageMetadata {
	t.Helper()
	probe := dial(t, socket, "history-check", "", false, true)
	defer probe.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, probe, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: room, After: "*", Limit: limit}}, IncludeOwn: true, MaxMessages: limit}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var messages []*irc.MessageMetadata
	for _, entry := range entries {
		if entry.Message != nil {
			messages = append(messages, entry.Message)
		}
	}
	return messages
}

// postNative posts text to room as a native guest and waits for its receipt.
func postNative(t *testing.T, socket, nick, room, text string) {
	t.Helper()
	author := dial(t, socket, nick, "", false, true)
	defer author.Close()
	id := nick + "-native"
	if err := author.SendWithID(room, text, id); err != nil {
		t.Fatal(err)
	}
	next(t, author, func(r *irc.SendReceiptEvent) bool { return r.RequestID == id })
}

// reserveUDPPort returns a port free on ip for network.
func reserveUDPPort(t *testing.T, network string, ip net.IP) int {
	t.Helper()
	probe, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.LocalAddr().(*net.UDPAddr).Port
}

func bindUDPPort(network string, ip net.IP) func(int) (io.Closer, error) {
	return func(port int) (io.Closer, error) { return net.ListenUDP(network, &net.UDPAddr{IP: ip, Port: port}) }
}
