//go:build darwin || linux

package gateway_test

// The encrypted network carriers in TestCarrierSuite: raw QUIC (bidirectional
// streams, paired unidirectional streams, datagrams), HTTP/3, WebTransport
// (bidirectional and unidirectional streams, datagrams). Every one but
// HTTP/3 carries the same
// correlated plain frame ({"version","correlation_id","profile","stream",
// "request"}), as a 4-byte length-prefixed frame on a stream or as AGD1
// fragments on a datagram channel, so one netFrameClient speaks them all over
// a per-carrier netTransport. The CoAP carriers are in
// carrier_suite_coap_test.go.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"
	wt "github.com/quic-go/webtransport-go"
)

var _ = registerSuiteCarriers(networkSuiteCarriers()...)

func networkSuiteCarriers() []suiteCarrier {
	quicCarrier := func(name, protocol, mode string, transport func(*quic.Conn) netTransport, wire func(*testing.T, *suiteEnv)) suiteCarrier {
		return suiteCarrier{name: name, listener: config.Listener{Protocol: protocol}, dial: dialQUICSuite(protocol == "rawquic_datagrams", transport), configure: suiteUDPAddress, secure: true, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", mode}, wire: wire}
	}
	wtCarrier := func(name, mode string, transport func(*wt.Transport, *wt.Session) netTransport, wire func(*testing.T, *suiteEnv)) suiteCarrier {
		return suiteCarrier{name: name, listener: config.Listener{Protocol: "webtransport"}, dial: dialWebTransportSuite(transport), configure: suiteUDPAddress, secure: true, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", mode}, wire: wire}
	}
	return []suiteCarrier{
		quicCarrier("quic", "rawquic", "quic_stream", quicBidi, quicSuiteWire),
		quicCarrier("quic_uni", "rawquic", "quic_uni_stream", quicUni, quicUniSuiteWire),
		quicCarrier("quic_datagrams", "rawquic_datagrams", "quic_datagrams", quicDatagrams, quicDatagramSuiteWire),
		wtCarrier("webtransport", "webtransport_stream", wtBidi, webTransportSuiteWire),
		wtCarrier("webtransport_uni", "webtransport_uni_stream", wtUni, webTransportUniSuiteWire),
		wtCarrier("webtransport_dgram", "webtransport_datagrams", wtDatagrams, webTransportDatagramSuiteWire),
		{name: "http3", listener: config.Listener{Protocol: "http3"}, dial: dialHTTP3Suite, configure: suiteUDPAddress, secure: true, results: true, reads: true, watch: true, wire: http3SuiteWire},
	}
}

// suiteUDPAddress gives a UDP listener a free loopback UDP port.
func suiteUDPAddress(t *testing.T, l *config.Listener) {
	probe, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.Address = probe.LocalAddr().String()
	_ = probe.Close()
}

// netFrame is one correlated plain frame; request is embedded verbatim, so it
// may be invalid JSON.
func netFrame(id string, request []byte, stream bool) []byte {
	return fmt.Appendf(nil, `{"version":1,"correlation_id":%q,"profile":"plain","stream":%t,"request":%s}`, id, stream, request)
}

// netTransport carries frames over one client connection. open writes frame
// as a new exchange and returns that exchange's responses: next returns the
// next response correlated to id, or nil once the exchange ends or wait
// passes; cancel abandons the exchange.
type netTransport interface {
	open(t *testing.T, id string, frame []byte) (next func(wait time.Duration) *adapter.FrameResponse, cancel func())
	close()
}

// netFrameClient is a suiteClient and suiteWatcher over any netTransport.
type netFrameClient struct {
	transport netTransport
	sequence  int
}

func (c *netFrameClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.sequence++
	id := fmt.Sprint("call-", c.sequence)
	next, cancel := c.transport.open(t, id, netFrame(id, request, false))
	defer cancel()
	// A frame that is not JSON cannot be correlated: stream carriers close
	// the stream and datagram carriers drop it, so the wait is short.
	wait := 10 * time.Second
	if !json.Valid(request) {
		wait = 2 * time.Second
	}
	response := next(wait)
	switch {
	case response == nil:
		return suiteReply{closed: true}
	case response.Version != 1 || response.CorrelationID != id:
		t.Fatalf("uncorrelated reply %+v", response)
	case response.Error != nil:
		if response.Event != "error" {
			t.Fatalf("failure event %+v", response)
		}
		return suiteReply{failure: response.Error}
	case response.Event != "result" || response.Result == nil:
		t.Fatalf("reply %+v", response)
	}
	return suiteReply{result: response.Result}
}

func (c *netFrameClient) send(t *testing.T, request []byte) {
	t.Helper()
	c.sequence++
	id := fmt.Sprint("send-", c.sequence)
	c.transport.open(t, id, netFrame(id, request, false))
}

func (c *netFrameClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	next, cancel := c.transport.open(t, "watch", netFrame("watch", request, true))
	if _, datagrams := c.transport.(*agd1Transport); datagrams {
		// A datagram exchange has no stream to cancel: the watch ends with
		// its connection.
		cancel = c.transport.close
	}
	sequence := uint64(0)
	return func() *backend.HistoryPage {
		t.Helper()
		response := next(10 * time.Second)
		if response == nil || response.Event == "end" || response.Error != nil {
			return nil
		}
		if response.Event != "page" || response.Page == nil || response.Sequence != 0 && response.Sequence != sequence+1 {
			t.Fatalf("watch frame %+v after sequence %d", response, sequence)
		}
		sequence = response.Sequence
		return response.Page
	}, cancel
}

func (c *netFrameClient) close() { c.transport.close() }

// netStream is one QUIC or WebTransport stream exchange.
type netStream struct {
	write    io.Writer
	finish   func() error // closes the write side
	read     io.Reader
	deadline func(time.Time) error
	cancel   func()
}

// streamExchange writes a length-prefixed frame on s, closes its write side
// and reads length-prefixed responses; receive, when set, accepts the stream
// the responses arrive on.
func streamExchange(t *testing.T, s netStream, frame []byte, receive func(context.Context) (netStream, error)) (func(time.Duration) *adapter.FrameResponse, func()) {
	t.Helper()
	cancel := s.cancel
	if _, err := s.write.Write(binary.BigEndian.AppendUint32(nil, uint32(len(frame)))); err == nil {
		_, _ = s.write.Write(frame)
	}
	_ = s.finish()
	return func(wait time.Duration) *adapter.FrameResponse {
		t.Helper()
		if receive != nil {
			ctx, stop := context.WithTimeout(t.Context(), wait)
			in, err := receive(ctx)
			stop()
			if err != nil {
				return nil
			}
			out := s
			s, cancel, receive = in, func() { out.cancel(); in.cancel() }, nil
		}
		_ = s.deadline(time.Now().Add(wait))
		var header [4]byte
		if _, err := io.ReadFull(s.read, header[:]); err != nil {
			return nil
		}
		size := binary.BigEndian.Uint32(header[:])
		if size == 0 || size > adapter.MaxResponseFrameBytes {
			t.Fatalf("response frame size %d", size)
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(s.read, body); err != nil {
			return nil
		}
		return decodeNetFrame(t, body)
	}, func() { cancel() }
}

func decodeNetFrame(t *testing.T, body []byte) *adapter.FrameResponse {
	t.Helper()
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("response frame %q: %v", body, err)
	}
	return &response
}

// agd1Transport carries frames as AGD1 fragments over a datagram channel.
type agd1Transport struct {
	sendDatagram    func([]byte) error
	receiveDatagram func(context.Context) ([]byte, error)
	closeConn       func()
	assembly        agd1Assembly
}

func (d *agd1Transport) open(t *testing.T, id string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
	t.Helper()
	var packet [16]byte
	if _, err := rand.Read(packet[:]); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range webTransportDatagrams(frame, packet) {
		if err := d.sendDatagram(fragment); err != nil {
			t.Fatal(err)
		}
	}
	return func(wait time.Duration) *adapter.FrameResponse { return d.receive(t, id, wait) }, func() {}
}

// receive returns the next response correlated to id, skipping others, or
// nil once wait passes.
func (d *agd1Transport) receive(t *testing.T, id string, wait time.Duration) *adapter.FrameResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), wait)
	defer cancel()
	for {
		data, err := d.receiveDatagram(ctx)
		if err != nil {
			return nil
		}
		if body := d.assembly.add(t, data); body != nil {
			if response := decodeNetFrame(t, body); response.CorrelationID == id {
				return response
			}
		}
	}
}

func (d *agd1Transport) close() { d.closeConn() }

// agd1Assembly reassembles AGD1 response fragments: a 28-byte header ("AGD1",
// a 16-byte message ID, the total length, the fragment index and count) and
// at most 972 payload bytes.
type agd1Assembly map[[16]byte][][]byte

func (a *agd1Assembly) add(t *testing.T, fragment []byte) []byte {
	t.Helper()
	if len(fragment) < webTransportDatagramHeader || len(fragment) > webTransportDatagramHeader+webTransportDatagramChunk || string(fragment[:4]) != "AGD1" {
		t.Fatalf("not an AGD1 fragment: %q", fragment)
	}
	var id [16]byte
	copy(id[:], fragment[4:20])
	total, index, count := int(binary.BigEndian.Uint32(fragment[20:24])), int(binary.BigEndian.Uint16(fragment[24:26])), int(binary.BigEndian.Uint16(fragment[26:28]))
	if total < 1 || total > adapter.MaxResponseFrameBytes || count != (total+webTransportDatagramChunk-1)/webTransportDatagramChunk || index >= count {
		t.Fatalf("AGD1 header total=%d index=%d count=%d", total, index, count)
	}
	if *a == nil {
		*a = agd1Assembly{}
	}
	parts := (*a)[id]
	if parts == nil {
		parts = make([][]byte, count)
		(*a)[id] = parts
	}
	if len(parts) != count {
		t.Fatalf("AGD1 fragment count changed")
	}
	parts[index] = append([]byte(nil), fragment[webTransportDatagramHeader:]...)
	body := make([]byte, 0, total)
	for _, part := range parts {
		if part == nil {
			return nil
		}
		body = append(body, part...)
	}
	delete(*a, id)
	if len(body) != total {
		t.Fatalf("AGD1 body %d bytes, header says %d", len(body), total)
	}
	return body
}

// randomFrameID is a fresh AGD1 message ID.
func randomFrameID(t *testing.T) [16]byte {
	t.Helper()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	return id
}

// QUIC: one connection per client; each exchange is a new stream, or the
// connection's datagrams.

type quicTransport struct {
	conn     *quic.Conn
	exchange func(*testing.T, string, []byte) (func(time.Duration) *adapter.FrameResponse, func())
}

func (q *quicTransport) open(t *testing.T, id string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
	return q.exchange(t, id, frame)
}

func (q *quicTransport) close() { _ = q.conn.CloseWithError(0, "done") }

func quicStream(s *quic.Stream) netStream {
	return netStream{write: s, finish: s.Close, read: s, deadline: s.SetReadDeadline, cancel: func() { s.CancelRead(0); s.CancelWrite(0) }}
}

func quicBidi(conn *quic.Conn) netTransport {
	return &quicTransport{conn: conn, exchange: func(t *testing.T, _ string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
		s, err := conn.OpenStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return streamExchange(t, quicStream(s), frame, nil)
	}}
}

// quicUni writes each request on a new unidirectional stream; its responses
// arrive on the next unidirectional stream the gateway opens.
func quicUni(conn *quic.Conn) netTransport {
	return &quicTransport{conn: conn, exchange: func(t *testing.T, _ string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
		out, err := conn.OpenUniStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		send := netStream{write: out, finish: out.Close, cancel: func() { out.CancelWrite(0) }}
		return streamExchange(t, send, frame, func(ctx context.Context) (netStream, error) {
			in, err := conn.AcceptUniStream(ctx)
			if err != nil {
				return netStream{}, err
			}
			return netStream{read: in, deadline: in.SetReadDeadline, cancel: func() { in.CancelRead(0) }}, nil
		})
	}}
}

func quicDatagrams(conn *quic.Conn) netTransport {
	d := &agd1Transport{sendDatagram: conn.SendDatagram, receiveDatagram: conn.ReceiveDatagram, closeConn: func() { _ = conn.CloseWithError(0, "done") }}
	return d
}

func quicSuiteTLS(d suiteDial) *tls.Config {
	config := d.tls.Clone()
	config.NextProtos = []string{adapter.QUICALPN}
	return config
}

func dialQUICSuite(datagrams bool, transport func(*quic.Conn) netTransport) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(t *testing.T, d suiteDial) (suiteClient, error) {
		ctx, cancel := suiteContext(t)
		defer cancel()
		conn, err := quic.DialAddr(ctx, d.address, quicSuiteTLS(d), &quic.Config{EnableDatagrams: datagrams, EnableStreamResetPartialDelivery: true})
		if err != nil {
			return nil, err
		}
		if state := conn.ConnectionState(); state.TLS.Version != tls.VersionTLS13 || state.TLS.NegotiatedProtocol != adapter.QUICALPN || datagrams && !(state.SupportsDatagrams.Local && state.SupportsDatagrams.Remote) {
			_ = conn.CloseWithError(0, "handshake")
			t.Fatalf("QUIC state %+v", state)
		}
		return &netFrameClient{transport: transport(conn)}, nil
	}
}

// WebTransport: one session per client at /v1/webtransport.

type wtTransport struct {
	transport *wt.Transport
	session   *wt.Session
	exchange  func(*testing.T, string, []byte) (func(time.Duration) *adapter.FrameResponse, func())
}

func (w *wtTransport) open(t *testing.T, id string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
	return w.exchange(t, id, frame)
}

func (w *wtTransport) close() {
	_ = w.session.CloseWithError(0, "done")
	_ = w.transport.Close()
}

func wtBidi(dialer *wt.Transport, session *wt.Session) netTransport {
	return &wtTransport{transport: dialer, session: session, exchange: func(t *testing.T, _ string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
		s, err := session.OpenStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return streamExchange(t, netStream{write: s, finish: s.Close, read: s, deadline: s.SetReadDeadline, cancel: func() { s.CancelRead(0); s.CancelWrite(0) }}, frame, nil)
	}}
}

func wtUni(dialer *wt.Transport, session *wt.Session) netTransport {
	return &wtTransport{transport: dialer, session: session, exchange: func(t *testing.T, _ string, frame []byte) (func(time.Duration) *adapter.FrameResponse, func()) {
		out, err := session.OpenUniStreamSync(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return streamExchange(t, netStream{write: out, finish: out.Close, cancel: func() { out.CancelWrite(0) }}, frame, func(ctx context.Context) (netStream, error) {
			in, err := session.AcceptUniStream(ctx)
			if err != nil {
				return netStream{}, err
			}
			return netStream{read: in, deadline: in.SetReadDeadline, cancel: func() { in.CancelRead(0) }}, nil
		})
	}}
}

func wtDatagrams(dialer *wt.Transport, session *wt.Session) netTransport {
	return &agd1Transport{sendDatagram: session.SendDatagram, receiveDatagram: session.ReceiveDatagram, closeConn: (&wtTransport{transport: dialer, session: session}).close}
}

func webTransportSuiteDialer(d suiteDial) *wt.Transport {
	config := d.tls.Clone()
	config.NextProtos = []string{http3.NextProtoH3}
	return &wt.Transport{TLSClientConfig: config, ApplicationProtocols: []string{adapter.WebSocketSubprotocol}, QUICConfig: &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true}}
}

func dialWebTransportSuite(transport func(*wt.Transport, *wt.Session) netTransport) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(t *testing.T, d suiteDial) (suiteClient, error) {
		ctx, cancel := suiteContext(t)
		defer cancel()
		dialer := webTransportSuiteDialer(d)
		response, session, err := dialer.Dial(ctx, "https://"+d.address+"/v1/webtransport", nil)
		if err != nil {
			_ = dialer.Close()
			return nil, err
		}
		if state := session.SessionState(); response.StatusCode != http.StatusOK || state.ApplicationProtocol != adapter.WebSocketSubprotocol || state.ConnectionState.TLS.Version != tls.VersionTLS13 || state.ConnectionState.TLS.NegotiatedProtocol != http3.NextProtoH3 {
			_ = dialer.Close()
			t.Fatalf("WebTransport handshake %d %+v", response.StatusCode, state)
		}
		return &netFrameClient{transport: transport(dialer, session)}, nil
	}
}

// HTTP/3 serves the HTTP contract: POST /v1/request takes any operation, and
// the JSON-RPC event stream POST /v1/rpc/events carries a watch.

type http3SuiteClient struct {
	base      string
	transport *http3.Transport
}

func dialHTTP3Suite(_ *testing.T, d suiteDial) (suiteClient, error) {
	return &http3SuiteClient{base: "https://" + d.address, transport: &http3.Transport{TLSClientConfig: d.tls.Clone()}}, nil
}

func (c *http3SuiteClient) post(t *testing.T, ctx context.Context, path string, body []byte, headers map[string]string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := (&http.Client{Transport: c.transport}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtoMajor != 3 || response.TLS == nil || response.TLS.Version != tls.VersionTLS13 || response.TLS.NegotiatedProtocol != http3.NextProtoH3 {
		t.Fatalf("HTTP/3 response protocol %d %+v", response.ProtoMajor, response.TLS)
	}
	return response
}

func (c *http3SuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	ctx, cancel := suiteContext(t)
	defer cancel()
	response := c.post(t, ctx, "/v1/request", request, nil)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return decodeSuiteReply(t, response.StatusCode < 300, data)
}

func (c *http3SuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *http3SuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal(request, &params); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	response := c.post(t, ctx, "/v1/rpc/events", rpcRequestBody(t, "watch", "airc.request", params), map[string]string{"Accept": "text/event-stream"})
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		cancel()
		t.Fatalf("HTTP/3 event stream: HTTP %d %s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	reader := bufio.NewReader(response.Body)
	return func() *backend.HistoryPage {
			t.Helper()
			return readSuiteSSEPage(t, reader)
		}, func() {
			cancel()
			_ = response.Body.Close()
		}
}

func (c *http3SuiteClient) close() { _ = c.transport.Close() }

// readSuiteSSEPage reads the next airc.page notification of a JSON-RPC event
// stream, or nil once the stream ends.
func readSuiteSSEPage(t *testing.T, reader *bufio.Reader) *backend.HistoryPage {
	t.Helper()
	var event, data string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return nil
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case line == "" && data != "":
			var notification struct {
				Method string `json:"method"`
				Params struct {
					ID   string              `json:"id"`
					Page backend.HistoryPage `json:"page"`
				} `json:"params"`
			}
			if event != "message" || json.Unmarshal([]byte(data), &notification) != nil || notification.Method != "airc.page" || notification.Params.ID != "watch" {
				return nil
			}
			return &notification.Params.Page
		}
	}
}

// Wire hooks.

func capabilitiesBody(t *testing.T, e *suiteEnv) []byte {
	t.Helper()
	return mustBody(t, plainRequest(map[string]any{"request_id": e.id("capabilities"), "operation": core.CapabilitiesGet}, e.guest("caps"), ""))
}

// suiteFrameBounds: capabilities report the correlated frame bounds.
func suiteFrameBounds(t *testing.T, e *suiteEnv) {
	r := e.dial(t).call(t, capabilitiesBody(t, e))
	if r.result == nil || r.result.Capabilities == nil || r.result.Capabilities.MaxRequestFrameBytes != adapter.MaxRequestFrameBytes || r.result.Capabilities.MaxResponseFrameBytes != adapter.MaxResponseFrameBytes {
		t.Fatalf("frame bounds: %v", r)
	}
}

// suiteCancelKeepsConnection: cancelling a streamed watch releases its
// native lease and leaves the client's connection usable.
func suiteCancelKeepsConnection(t *testing.T, e *suiteEnv) {
	client := e.dial(t)
	baseline := nativeObserverCount(t, e.statusClient)
	pages, cancel := client.(suiteWatcher).watch(t, watchBody(t, e.id("cancel"), "#wire", e.guest("cancel")))
	if pages() == nil {
		t.Fatal("watch did not start")
	}
	waitNativeObserverCount(t, e.statusClient, baseline+1)
	cancel()
	waitNativeObserverCountAtMost(t, e.statusClient, baseline)
	if r := client.call(t, capabilitiesBody(t, e)); r.result == nil || r.result.Capabilities == nil {
		t.Fatalf("connection unusable after a cancelled watch: %v", r)
	}
}

// suiteLargePages: a history page larger than a request frame is delivered
// whole, equal to aircd's records, over the carrier's fragmentation (AGD1,
// CoAP Block2, notifications); with watch, so is a retained watch page, and a
// live page carries the whole record aircd holds.
func suiteLargePages(t *testing.T, e *suiteEnv, watch bool) {
	author := dial(t, e.socket, e.guest("large"), "", false, true)
	defer author.Close()
	for i := range 7 {
		text := fmt.Sprintf("large %d %s", i, strings.Repeat("世界 🙂", 260))
		if err := author.SendWithID("#wire", text, e.id("large")); err != nil {
			t.Fatal(err)
		}
		e.observed.wait(t, text)
	}
	native := nativeWebTransportMessages(t, e.socket, "#wire")
	whole := func(name string, messages []*irc.MessageMetadata) {
		t.Helper()
		if len(native) < 7 || !reflect.DeepEqual(messages, native) || len(mustBody(t, messages)) <= adapter.MaxRequestFrameBytes {
			t.Fatalf("large %s page differs from aircd's %d records: %d messages", name, len(native), len(messages))
		}
	}
	fields := plainRequest(map[string]any{"request_id": e.id("large"), "operation": core.HistoryRead, "target": map[string]any{"kind": "room", "name": "#wire"}, "start": "retained", "limit": 16}, e.guest("reader"), "")
	r := e.dial(t).call(t, mustBody(t, fields))
	if r.result == nil || r.result.Page == nil {
		t.Fatalf("large history: %v", r)
	}
	whole("history", r.result.Page.Messages)
	if !watch {
		return
	}
	fields["operation"], fields["request_id"] = core.Watch, e.id("large")
	pages, cancel := e.dial(t).(suiteWatcher).watch(t, mustBody(t, fields))
	defer cancel()
	page := pages()
	if page == nil {
		t.Fatal("large watch did not start")
	}
	whole("watch", page.Messages)
	text := "live record via " + e.carrier.name
	if err := author.SendWithID("#wire", text, e.id("live")); err != nil {
		t.Fatal(err)
	}
	var live *irc.MessageMetadata
	for live == nil {
		if page = pages(); page == nil {
			t.Fatal("watch ended before the live message")
		}
		for _, m := range page.Messages {
			if m.Message == text {
				live = m
			}
		}
	}
	records := nativeWebTransportMessages(t, e.socket, "#wire")
	want := *records[len(records)-1]
	// A live record comes from aircd's OBSERVE, which carries no CHECK
	// sequence or receipt.
	if live.Seq != 0 || live.Receipt != nil || want.Seq == 0 {
		t.Fatalf("live record %+v, aircd holds %+v", live, want)
	}
	want.Seq, want.Receipt = 0, nil
	if !reflect.DeepEqual(live, &want) {
		t.Fatalf("live record %+v, aircd holds %+v", live, want)
	}
}

// agd1SuiteWire covers AGD1 reassembly on a datagram carrier: fragments in
// any order with duplicates assemble once, and an incomplete or conflicting
// assembly is never dispatched and leaves the client usable.
func agd1SuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("reordered_duplicate_fragments", func(t *testing.T) {
		d := e.dial(t).(*netFrameClient).transport.(*agd1Transport)
		id, nick, text := e.id("reordered"), e.guest("reordered"), e.text("reordered")+strings.Repeat("x", 3500)
		fragments := webTransportDatagrams(netFrame(id, sendBody(t, id, "#wire", text, nick, ""), false), randomFrameID(t))
		if len(fragments) < 3 {
			t.Fatal("request did not fragment")
		}
		for i := len(fragments) - 1; i >= 0; i-- {
			if err := d.sendDatagram(fragments[i]); err != nil {
				t.Fatal(err)
			}
			if i == len(fragments)-1 {
				if err := d.sendDatagram(fragments[i]); err != nil {
					t.Fatal(err)
				}
			}
		}
		r := d.receive(t, id, 10*time.Second)
		if r == nil || r.Result == nil || r.Result.Result == nil || r.Result.Result.RequestID != id || !r.Result.Result.Durable {
			t.Fatalf("reordered request: %+v", r)
		}
		e.posted(t, id, "#wire", text, nick, "")
	})
	t.Run("incomplete_and_conflicting_fragments", func(t *testing.T) {
		client := e.dial(t).(*netFrameClient)
		d := client.transport.(*agd1Transport)
		for _, conflicting := range []bool{false, true} {
			id := e.id("incomplete")
			fragments := webTransportDatagrams(netFrame(id, sendBody(t, id, "#wire", strings.Repeat("must not post ", 300), e.guest("partial"), ""), false), randomFrameID(t))
			if err := d.sendDatagram(fragments[0]); err != nil {
				t.Fatal(err)
			}
			if conflicting {
				changed := append([]byte(nil), fragments[0]...)
				changed[len(changed)-1] ^= 1
				if err := d.sendDatagram(changed); err != nil {
					t.Fatal(err)
				}
			}
			if r := d.receive(t, id, 1500*time.Millisecond); r != nil {
				t.Fatalf("incomplete assembly answered: %+v", r)
			}
			e.noJob(t, id)
		}
		if r := client.call(t, capabilitiesBody(t, e)); r.result == nil || r.result.Capabilities == nil {
			t.Fatalf("client unusable after an incomplete assembly: %v", r)
		}
	})
}

// quicHandshakeRefusals: a QUIC listener refuses an untrusted certificate, a
// wrong server name and an unknown ALPN (no_application_protocol).
func quicHandshakeRefusals(t *testing.T, e *suiteEnv, alpn string) {
	for name, configure := range map[string]func(*tls.Config){
		"untrusted":  func(c *tls.Config) { c.RootCAs = nil },
		"wrong_name": func(c *tls.Config) { c.ServerName = "wrong.example" },
		"wrong_alpn": func(c *tls.Config) { c.NextProtos = []string{"h3"} },
	} {
		config := e.cert.Client.Clone()
		config.NextProtos = []string{alpn}
		configure(config)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		conn, err := quic.DialAddr(ctx, e.address, config, &quic.Config{})
		cancel()
		if err == nil {
			_ = conn.CloseWithError(0, "refusal")
			t.Fatalf("%s: QUIC handshake accepted", name)
		}
		var hostname x509.HostnameError
		var unknown x509.UnknownAuthorityError
		var transport *quic.TransportError
		if name == "wrong_name" && !errors.As(err, &hostname) || name == "untrusted" && !errors.As(err, &unknown) || name == "wrong_alpn" && !(errors.As(err, &transport) && transport.ErrorCode == 0x178) {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func quicSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	t.Run("handshake_refusals", func(t *testing.T) { quicHandshakeRefusals(t, e, adapter.QUICALPN) })
	t.Run("partial_and_empty_frames", func(t *testing.T) {
		// A stream that ends inside a frame, or carries a zero-length
		// frame, is closed without a response; the connection stays usable.
		client := e.dial(t).(*netFrameClient)
		conn := client.transport.(*quicTransport).conn
		for _, raw := range [][]byte{{0, 0, 1, 0, '{'}, {0, 0, 0, 0}} {
			stream, err := conn.OpenStreamSync(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Write(raw); err != nil {
				t.Fatal(err)
			}
			_ = stream.Close()
			_ = stream.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
			if n, err := stream.Read(make([]byte, 1)); err == nil || n != 0 {
				t.Fatalf("frame %x answered", raw)
			}
		}
		if r := client.call(t, capabilitiesBody(t, e)); r.result == nil || r.result.Capabilities == nil {
			t.Fatalf("connection unusable after a partial frame: %v", r)
		}
	})
	t.Run("cancel_keeps_connection", func(t *testing.T) { suiteCancelKeepsConnection(t, e) })
}

func quicUniSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	t.Run("cancel_keeps_connection", func(t *testing.T) { suiteCancelKeepsConnection(t, e) })
}

func quicDatagramSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	t.Run("requires_datagrams", func(t *testing.T) {
		// A peer that does not negotiate datagrams is closed. The close can
		// reach the client before its handshake completes, as the transport
		// error APPLICATION_ERROR (RFC 9000, section 10.2.3).
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		conn, err := quic.DialAddr(ctx, e.address, quicSuiteTLS(suiteDial{tls: e.cert.Client}), &quic.Config{})
		var early *quic.TransportError
		var closed *quic.ApplicationError
		if errors.As(err, &early) && early.Remote && early.ErrorCode == quic.ApplicationErrorErrorCode || errors.As(err, &closed) && closed.Remote {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		defer conn.CloseWithError(0, "done")
		select {
		case <-conn.Context().Done():
		case <-ctx.Done():
			t.Fatal("datagram listener kept a peer without datagrams")
		}
	})
	agd1SuiteWire(t, e)
}

func webTransportSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	t.Run("handshake_refusals", func(t *testing.T) {
		url := "https://" + e.address + "/v1/webtransport"
		for _, c := range []struct {
			name      string
			configure func(*tls.Config, *wt.Transport) (string, http.Header)
		}{
			{"untrusted", func(c *tls.Config, _ *wt.Transport) (string, http.Header) { c.RootCAs = nil; return url, nil }},
			{"wrong_name", func(c *tls.Config, _ *wt.Transport) (string, http.Header) {
				c.ServerName = "wrong.example"
				return url, nil
			}},
			{"no_application_protocol", func(_ *tls.Config, w *wt.Transport) (string, http.Header) {
				w.ApplicationProtocols = nil
				return url, nil
			}},
			{"wrong_application_protocol", func(_ *tls.Config, w *wt.Transport) (string, http.Header) {
				w.ApplicationProtocols = []string{"wrong.protocol"}
				return url, nil
			}},
			{"malformed_application_protocol", func(*tls.Config, *wt.Transport) (string, http.Header) {
				return url, http.Header{"WT-Available-Protocols": {"["}}
			}},
			{"forbidden_origin", func(*tls.Config, *wt.Transport) (string, http.Header) {
				return url, http.Header{"Origin": {"https://evil.example"}}
			}},
			{"query", func(*tls.Config, *wt.Transport) (string, http.Header) { return url + "?unexpected=1", nil }},
			{"authorization", func(*tls.Config, *wt.Transport) (string, http.Header) {
				return url, http.Header{"Authorization": {"Bearer forbidden-on-connect"}}
			}},
			{"idempotency", func(*tls.Config, *wt.Transport) (string, http.Header) {
				return url, http.Header{"Idempotency-Key": {"forbidden-on-connect"}}
			}},
		} {
			dialer := webTransportSuiteDialer(suiteDial{tls: e.cert.Client})
			target, headers := c.configure(dialer.TLSClientConfig, dialer)
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			response, session, err := dialer.Dial(ctx, target, headers)
			cancel()
			_ = dialer.Close()
			if err == nil || session != nil || response != nil && response.StatusCode == http.StatusOK {
				t.Fatalf("%s: WebTransport session accepted", c.name)
			}
		}
	})
	t.Run("cancel_keeps_connection", func(t *testing.T) { suiteCancelKeepsConnection(t, e) })
	t.Run("identify_spans_session", func(t *testing.T) {
		// account.identify on a bidirectional stream holds for the
		// session's unidirectional streams and datagrams.
		client := e.dial(t).(*netFrameClient)
		w := client.transport.(*wtTransport)
		if r := client.call(t, mustBody(t, plainRequest(map[string]any{"request_id": e.id("identify"), "operation": core.AccountIdentify}, "alice", aliceToken))); r.result == nil || r.result.Account == nil {
			t.Fatalf("identify: %v", r)
		}
		for _, path := range []struct {
			name      string
			transport netTransport
		}{{"uni", wtUni(w.transport, w.session)}, {"datagrams", wtDatagrams(w.transport, w.session)}} {
			id, text := e.id(path.name), e.text(path.name)
			e.admitted(t, (&netFrameClient{transport: path.transport}).call(t, sendBody(t, id, "#wire", text, "", "")), id)
			e.posted(t, id, "#wire", text, "alice", aliceToken)
		}
	})
}

func webTransportUniSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	t.Run("cancel_keeps_connection", func(t *testing.T) { suiteCancelKeepsConnection(t, e) })
}

func webTransportDatagramSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
	agd1SuiteWire(t, e)
}

func http3SuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
	t.Run("handshake_refusals", func(t *testing.T) {
		for name, configure := range map[string]func(*tls.Config){
			"untrusted":  func(c *tls.Config) { c.RootCAs = nil },
			"wrong_name": func(c *tls.Config) { c.ServerName = "wrong.example" },
			"tls12":      func(c *tls.Config) { c.MinVersion, c.MaxVersion = tls.VersionTLS12, tls.VersionTLS12 },
		} {
			config := e.cert.Client.Clone()
			configure(config)
			transport := &http3.Transport{TLSClientConfig: config}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+e.address+"/v1/request", bytes.NewReader(capabilitiesBody(t, e)))
			response, err := (&http.Client{Transport: transport}).Do(request)
			cancel()
			_ = transport.Close()
			if err == nil {
				_ = response.Body.Close()
				t.Fatalf("%s: HTTP/3 request answered", name)
			}
		}
	})
	t.Run("header_boundaries", func(t *testing.T) {
		// The JSON-RPC route answers without author headers under the
		// listener's policy and refuses a malformed or repeated nickname,
		// an unconfigured Origin and a query string.
		client := e.dial(t).(*http3SuiteClient)
		body := rpcRequestBody(t, "capabilities", "airc.capabilities", map[string]any{})
		for _, c := range []struct {
			name, path string
			header     http.Header
			want       int
		}{
			{"no_author", "/v1/rpc", nil, http.StatusOK},
			{"bad_nick", "/v1/rpc", http.Header{"Airc-Nick": {"bad nick"}}, http.StatusBadRequest},
			{"repeated_nick", "/v1/rpc", http.Header{"Airc-Nick": {e.guest("h3"), e.guest("h3")}}, http.StatusBadRequest},
			{"origin", "/v1/rpc", http.Header{"Airc-Nick": {e.guest("h3")}, "Origin": {"https://unconfigured.example"}}, http.StatusForbidden},
			{"query", "/v1/rpc?unexpected=1", http.Header{"Airc-Nick": {e.guest("h3")}}, http.StatusForbidden},
		} {
			ctx, cancel := suiteContext(t)
			request, _ := http.NewRequestWithContext(ctx, http.MethodPost, client.base+c.path, bytes.NewReader(body))
			request.Header = c.header.Clone()
			if request.Header == nil {
				request.Header = http.Header{}
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Transport: client.transport}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			cancel()
			if response.StatusCode != c.want || response.ProtoMajor != 3 {
				t.Fatalf("%s: HTTP/%d %d, want %d", c.name, response.ProtoMajor, response.StatusCode, c.want)
			}
		}
	})
	t.Run("live_events", func(t *testing.T) {
		// The classic live stream is served over HTTP/3 too, and its lease
		// ends with the request.
		client := e.dial(t).(*http3SuiteClient)
		baseline := nativeObserverCount(t, e.statusClient)
		defer waitNativeObserverCountAtMost(t, e.statusClient, baseline)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, client.base+"/v1/live/events?kind=room&target=%23wire&limit=8", nil)
		request.Header.Set("AIRC-Nick", e.guest("live"))
		request.Header.Set("Accept", "text/event-stream")
		response, err := (&http.Client{Transport: client.transport}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		reader := bufio.NewReader(response.Body)
		line, err := reader.ReadString('\n')
		for err == nil && !strings.HasPrefix(line, "event:") {
			line, err = reader.ReadString('\n')
		}
		if response.StatusCode != http.StatusOK || response.ProtoMajor != 3 || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") || err != nil || strings.TrimSpace(line) != "event: page" {
			t.Fatalf("live events: HTTP/%d %d %q %q %v", response.ProtoMajor, response.StatusCode, response.Header.Get("Content-Type"), line, err)
		}
	})
}
