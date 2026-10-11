//go:build darwin || linux

package gateway_test

// The CoAP carriers in TestCarrierSuite: CoAP over DTLS (coaps, confirmable
// and non-confirmable), over TLS (coaps_tcp), and plain over UDP and TCP. A
// request is a plain frame POSTed to /v1/coap/rpc and its outcome is the
// response body; a streamed watch registers on /v1/coap/observe/register and
// observes /v1/coap/observe, which notifies AGD1 fragments of each page.

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapNetClient "github.com/plgd-dev/go-coap/v3/net/client"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapTCPClient "github.com/plgd-dev/go-coap/v3/tcp/client"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
)

var _ = registerSuiteCarriers(
	suiteCarrier{name: "coaps", listener: config.Listener{Protocol: "coaps"}, dial: dialCoAPSSuite(message.Confirmable), secure: true, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "coaps_observe_fragments"}, wire: coapsSuiteWire},
	suiteCarrier{name: "coaps_non", listener: config.Listener{Protocol: "coaps"}, dial: dialCoAPSSuite(message.NonConfirmable), secure: true, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "coaps_observe_fragments"}},
	suiteCarrier{name: "coaps_tcp", listener: config.Listener{Protocol: "coaps_tcp"}, dial: dialCoAPTCPSuite, secure: true, tls: true, alpn: coapTCPALPN, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "coaps_tcp_observe_fragments"}, wire: coapTCPSuiteWire},
	suiteCarrier{name: "coap_tcp", listener: config.Listener{Protocol: "coap_tcp"}, dial: dialCoAPTCPSuite, results: true, reads: true, session: true, watch: true, watchModes: []string{"long_poll", "coaps_tcp_observe_fragments"}, wire: coapPlainSuiteWire},
	// Observe over plain UDP delivers its ready marker but no page, so the
	// watch scenarios are off; its long-poll watch keeps a focused test.
	suiteCarrier{name: "coap_udp", listener: config.Listener{Protocol: "coap_udp"}, dial: dialCoAPUDPSuite, results: true, reads: true, wire: coapUDPSuiteWire},
)

// coapSuiteConn is what the suite uses of a go-coap UDP, DTLS or TCP client.
type coapSuiteConn interface {
	NewPostRequest(ctx context.Context, path string, contentFormat message.MediaType, payload io.ReadSeeker, opts ...message.Option) (*pool.Message, error)
	NewGetRequest(ctx context.Context, path string, opts ...message.Option) (*pool.Message, error)
	Do(*pool.Message) (*pool.Message, error)
	DoObserve(*pool.Message, func(*pool.Message)) (coapNetClient.Observation, error)
	ReleaseMessage(*pool.Message)
	Done() <-chan struct{}
	Close() error
}

type coapSuiteClient struct {
	conn coapSuiteConn
	// nonConfirmable sends every request as a NON message.
	nonConfirmable bool
	sequence       int
}

func dialCoAPSSuite(typ message.Type) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(t *testing.T, d suiteDial) (suiteClient, error) {
		conn, err := dialCoAPSPeer(t, d.address, d.tls, coapALPN)
		if err != nil {
			return nil, err
		}
		return &coapSuiteClient{conn: conn, nonConfirmable: typ == message.NonConfirmable}, nil
	}
}

// dialCoAPSPeer completes a CoAP DTLS handshake trusting trust's roots.
func dialCoAPSPeer(t *testing.T, address string, trust *tls.Config, alpn string) (coapSuiteConn, error) {
	t.Helper()
	conn, err := coapdtls.Dial(address, coapdtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(trust.RootCAs), piondtls.WithServerName(cmpServerName(trust)), piondtls.WithSupportedProtocols(alpn),
		piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
		piondtls.WithCipherSuites(piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, piondtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384), piondtls.WithMTU(1200),
	), coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second))
	if err != nil {
		return nil, err
	}
	raw := conn.NetConn().(*piondtls.Conn)
	ctx, cancel := suiteContext(t)
	defer cancel()
	if err := raw.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if state, ok := raw.ConnectionState(); !ok || state.NegotiatedProtocol != alpn || len(state.PeerCertificates) == 0 {
		_ = conn.Close()
		t.Fatalf("CoAPS state %+v", state)
	}
	return conn, nil
}

func dialCoAPTCPSuite(t *testing.T, d suiteDial) (suiteClient, error) {
	ctx, cancel := suiteContext(t)
	defer cancel()
	if d.tls != nil {
		d.tls.NextProtos = []string{coapTCPALPN}
	}
	raw, err := suiteConnect("tcp", d)
	if err != nil {
		return nil, err
	}
	if secure, ok := raw.(*tls.Conn); ok {
		if state := secure.ConnectionState(); state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != coapTCPALPN || len(state.VerifiedChains) == 0 {
			_ = raw.Close()
			t.Fatalf("CoAP TLS state %+v", state)
		}
	}
	return &coapSuiteClient{conn: startCoAPTCPClient(t, ctx, raw, 1152, func(*coapTCPClient.Conn, *pool.Message) (bool, error) { return false, nil })}, nil
}

func dialCoAPUDPSuite(_ *testing.T, d suiteDial) (suiteClient, error) {
	conn, err := coapUDP.Dial(d.address, coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second))
	if err != nil {
		return nil, err
	}
	return &coapSuiteClient{conn: conn}, nil
}

// post sends body to path and returns the response code and payload; a nil
// error with code 0 never happens.
func (c *coapSuiteClient) post(t *testing.T, path string, body []byte) (codes.Code, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	request, err := c.conn.NewPostRequest(ctx, path, message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if c.nonConfirmable {
		request.SetType(message.NonConfirmable)
	}
	response, err := c.conn.Do(request)
	c.conn.ReleaseMessage(request)
	if err != nil {
		return 0, nil, err
	}
	defer c.conn.ReleaseMessage(response)
	payload, err := response.ReadBody()
	if err != nil {
		return 0, nil, err
	}
	return response.Code(), payload, nil
}

func (c *coapSuiteClient) call(t *testing.T, request []byte) suiteReply {
	t.Helper()
	c.sequence++
	id := fmt.Sprint("call-", c.sequence)
	code, payload, err := c.post(t, "/v1/coap/rpc", netFrame(id, request, false))
	switch {
	case err != nil:
		t.Logf("CoAP exchange ended: %v", err)
		return suiteReply{closed: true}
	case len(payload) == 0:
		// A frame the gateway cannot parse is refused with a bare 4.00.
		if code != codes.BadRequest {
			t.Fatalf("CoAP %v without a body", code)
		}
		return suiteReply{closed: true}
	}
	response := decodeNetFrame(t, payload)
	switch {
	case response.Version != 1 || response.CorrelationID != id:
		t.Fatalf("uncorrelated reply %+v", response)
	case response.Error != nil:
		// A refusal is a correlated error frame in a 4.xx response.
		if code < codes.BadRequest || response.Event != "error" {
			t.Fatalf("CoAP %v refusal %+v", code, response)
		}
		return suiteReply{failure: response.Error}
	case code != codes.Content:
		t.Fatalf("CoAP %v result %+v", code, response)
	case response.Event != "result" || response.Sequence != 1 || response.Result == nil:
		t.Fatalf("reply %+v", response)
	}
	return suiteReply{result: response.Result}
}

func (c *coapSuiteClient) send(t *testing.T, request []byte) { c.call(t, request) }

func (c *coapSuiteClient) close() { _ = c.conn.Close() }

// watch registers the watch, observes it with the registration's token and
// reassembles each notified page.
func (c *coapSuiteClient) watch(t *testing.T, request []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	return c.observe(t, c.register(t, request))
}

// register registers a streamed watch under correlation ID "watch" and
// returns its observation token.
func (c *coapSuiteClient) register(t *testing.T, request []byte) []byte {
	t.Helper()
	code, payload, err := c.post(t, "/v1/coap/observe/register", netFrame("watch", request, true))
	var registration struct {
		Version          int    `json:"version"`
		CorrelationID    string `json:"correlation_id"`
		ObservationToken string `json:"observation_token"`
		ExpiresInMS      int    `json:"expires_in_ms"`
	}
	if err != nil || code != codes.Content || json.Unmarshal(payload, &registration) != nil || registration.Version != 1 || registration.CorrelationID != "watch" || len(registration.ObservationToken) != 16 || registration.ExpiresInMS != 10000 {
		t.Fatalf("CoAP observe registration %v %q: %v", code, payload, err)
	}
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// observe observes a registered watch: the first notification confirms the
// registration and each later one is an AGD1 fragment of a page.
func (c *coapSuiteClient) observe(t *testing.T, token []byte) (func() *backend.HistoryPage, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	observe, err := c.conn.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observe.SetToken(token)
	observe.SetObserve(0)
	type note struct {
		format   message.MediaType
		body     []byte
		sequence uint32
		observe  bool
	}
	notifications := make(chan note, 64)
	observation, err := c.conn.DoObserve(observe, func(notification *pool.Message) {
		body, err := notification.ReadBody()
		if err != nil {
			return
		}
		format, _ := notification.ContentFormat()
		sequence, observeErr := notification.Observe()
		select {
		case notifications <- note{format, append([]byte(nil), body...), sequence, observeErr == nil}:
		default:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	next := func() (note, bool) {
		select {
		case n := <-notifications:
			if !n.observe {
				t.Fatal("notification without Observe")
			}
			return n, true
		case <-c.conn.Done():
		case <-time.After(10 * time.Second):
		}
		return note{}, false
	}
	// The observation first confirms, as Observe 0, the registration it
	// took over.
	if ready, ok := next(); !ok || ready.sequence != 0 || ready.format != message.AppJSON || !bytes.Contains(ready.body, []byte(`"event":"ready"`)) || !bytes.Contains(ready.body, []byte(`"correlation_id":"watch"`)) {
		t.Fatalf("observe ready marker %+v", ready)
	}
	var assembly agd1Assembly
	sequence := uint64(0)
	return func() *backend.HistoryPage {
			t.Helper()
			for {
				n, ok := next()
				if !ok {
					return nil
				}
				if n.format != message.AppOctets {
					t.Fatalf("notification format %v", n.format)
				}
				body := assembly.add(t, n.body)
				if body == nil {
					continue
				}
				response := decodeNetFrame(t, body)
				if response.Event == "end" || response.Error != nil {
					return nil
				}
				if response.CorrelationID != "watch" || response.Event != "page" || response.Page == nil || response.Sequence != sequence+1 {
					t.Fatalf("notified frame %+v after sequence %d", response, sequence)
				}
				sequence = response.Sequence
				return response.Page
			}
		}, func() {
			defer cancel()
			stop, done := context.WithTimeout(context.Background(), 3*time.Second)
			defer done()
			if err := observation.Cancel(stop); err != nil {
				t.Logf("observe cancel: %v", err)
			}
		}
}

// Wire hooks.

// coapSuiteWire covers what every CoAP carrier shares: frame bounds, frames
// refused with a bare 4.00 before intake, and Block2 and notification
// fragmentation of a page larger than a request frame.
func coapSuiteWire(observe bool, extra func(*testing.T, *suiteEnv)) func(*testing.T, *suiteEnv) {
	return func(t *testing.T, e *suiteEnv) {
		t.Run("large_pages", func(t *testing.T) { suiteLargeHistory(t, e, observe) })
		t.Run("frame_bounds", func(t *testing.T) { suiteFrameBounds(t, e) })
		t.Run("malformed_frames", func(t *testing.T) {
			client := e.dial(t).(*coapSuiteClient)
			id := e.id("malformed")
			request := sendBody(t, id, "#wire", "must not post", e.guest("malformed"), "")
			for name, frame := range map[string][]byte{
				"array_request":   []byte(`{"version":1,"correlation_id":"array","profile":"plain","request":[]}`),
				"empty_request":   []byte(`{"version":1,"correlation_id":"empty","profile":"plain","request":{}}`),
				"missing_request": []byte(`{"version":1,"correlation_id":"missing"}`),
				"unknown_profile": fmt.Appendf(nil, `{"version":1,"correlation_id":"profile","profile":"unknown","request":%s}`, request),
				"stream_on_rpc":   netFrame("stream", request, true),
			} {
				if code, payload, err := client.post(t, "/v1/coap/rpc", frame); err != nil || code != codes.BadRequest || len(payload) != 0 {
					t.Fatalf("%s: %v %q %v", name, code, payload, err)
				}
			}
			e.noJob(t, id)
			if r := client.call(t, capabilitiesBody(t, e)); r.result == nil || r.result.Capabilities == nil {
				t.Fatalf("client unusable after refused frames: %v", r)
			}
		})
		if extra != nil {
			extra(t, e)
		}
	}
}

var coapsSuiteWire = coapSuiteWire(true, func(t *testing.T, e *suiteEnv) {
	t.Run("handshake_refusals", func(t *testing.T) {
		for name, configure := range map[string]func(*tls.Config) string{
			"untrusted":  func(c *tls.Config) string { c.RootCAs = x509.NewCertPool(); return coapALPN },
			"wrong_name": func(c *tls.Config) string { c.ServerName = "wrong.example"; return coapALPN },
			"wrong_alpn": func(*tls.Config) string { return "wrong-alpn" },
		} {
			trust := e.cert.Client.Clone()
			if conn, err := dialCoAPSPeer(t, e.address, trust, configure(trust)); err == nil {
				_ = conn.Close()
				t.Fatalf("%s: CoAPS handshake accepted", name)
			}
		}
	})
	t.Run("registration_holds_no_lease", func(t *testing.T) {
		// Registering takes no native lease until the observation starts.
		baseline := nativeObserverCount(t, e.statusClient)
		e.dial(t).(*coapSuiteClient).register(t, watchBody(t, e.id("registered"), "#wire", e.guest("reg")))
		time.Sleep(100 * time.Millisecond)
		if got := nativeObserverCount(t, e.statusClient); got != baseline {
			t.Fatalf("registration took a native lease: %d, baseline %d", got, baseline)
		}
	})
	t.Run("token_bound_to_association", func(t *testing.T) {
		// An observation token is useless on another DTLS association.
		token := e.dial(t).(*coapSuiteClient).register(t, watchBody(t, e.id("owner"), "#wire", e.guest("owner")))
		other := e.dial(t).(*coapSuiteClient)
		ctx, cancel := suiteContext(t)
		defer cancel()
		request, err := other.conn.NewGetRequest(ctx, "/v1/coap/observe")
		if err != nil {
			t.Fatal(err)
		}
		request.SetToken(token)
		request.SetObserve(0)
		observation, err := other.conn.DoObserve(request, func(n *pool.Message) { _, _ = n.ReadBody() })
		if err == nil || !strings.Contains(err.Error(), "BadRequest") {
			if observation != nil {
				_ = observation.Cancel(ctx)
			}
			t.Fatalf("observation token transferred: %v", err)
		}
	})
	t.Run("correlation_collision", func(t *testing.T) {
		// A request reusing a pending registration's correlation ID closes
		// the peer before intake; the same intent on a fresh peer posts.
		client := e.dial(t).(*coapSuiteClient)
		client.register(t, watchBody(t, e.id("pending"), "#wire", e.guest("pending")))
		id, nick, text := e.id("collision"), e.guest("collision"), e.text("collision")
		body := sendBody(t, id, "#wire", text, nick, "")
		if code, payload, err := client.post(t, "/v1/coap/rpc", netFrame("watch", body, false)); err == nil {
			t.Fatalf("collision answered %v %q", code, payload)
		}
		e.noJob(t, id)
		e.admitted(t, e.dial(t).call(t, body), id)
		e.posted(t, id, "#wire", text, nick, "")
	})
})

var coapTCPSuiteWire = coapSuiteWire(true, func(t *testing.T, e *suiteEnv) {
	t.Run("wrong_alpn", func(t *testing.T) {
		config := e.cert.Client.Clone()
		config.NextProtos = []string{"wrong"}
		if conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", e.secure, config); err == nil {
			_ = conn.Close()
			t.Fatal("TLS handshake with an unknown ALPN accepted")
		}
	})
})

var (
	coapPlainSuiteWire = coapSuiteWire(true, nil)
	coapUDPSuiteWire   = coapSuiteWire(false, nil)
)
