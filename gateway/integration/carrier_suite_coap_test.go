//go:build darwin || linux

package gateway_test

// The CoAP carrier in TestCarrierSuite: CoAP over UDP, with confirmable and
// with non-confirmable requests. A request is a plain frame POSTed to
// /v1/coap/rpc and its outcome is the response body; a streamed watch
// registers on /v1/coap/observe/register and observes /v1/coap/observe, which
// notifies AGD1 fragments of each page.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

var _ = registerSuiteCarriers(
	suiteCarrier{name: "coap_udp", listener: config.Listener{Protocol: "coap_udp"}, dial: dialCoAPUDPSuite(message.Confirmable), configure: suiteUDPAddress, results: true, reads: true, watch: true, watchModes: []string{"long_poll", "coap_udp_observe_fragments"}, wire: coapUDPSuiteWire},
	suiteCarrier{name: "coap_udp_non", listener: config.Listener{Protocol: "coap_udp"}, dial: dialCoAPUDPSuite(message.NonConfirmable), configure: suiteUDPAddress, results: true, reads: true, watch: true, watchModes: []string{"long_poll", "coap_udp_observe_fragments"}},
)

type coapSuiteClient struct {
	conn *coapClient.Conn
	// nonConfirmable sends every request as a NON message.
	nonConfirmable bool
	sequence       int
}

func dialCoAPUDPSuite(typ message.Type) func(*testing.T, suiteDial) (suiteClient, error) {
	return func(_ *testing.T, d suiteDial) (suiteClient, error) {
		conn, err := coapUDP.Dial(d.address, coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second))
		if err != nil {
			return nil, err
		}
		return &coapSuiteClient{conn: conn, nonConfirmable: typ == message.NonConfirmable}, nil
	}
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

// coapUDPSuiteWire covers frame bounds, frames refused with a bare 4.00
// before intake, Block2 and notification fragmentation of a page larger than
// a request frame, and the scope of an Observe registration.
func coapUDPSuiteWire(t *testing.T, e *suiteEnv) {
	t.Run("large_pages", func(t *testing.T) { suiteLargePages(t, e, true) })
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
	t.Run("registration_holds_no_lease", func(t *testing.T) {
		// Registering takes no native lease until the observation starts.
		baseline := nativeObserverCount(t, e.statusClient)
		client := e.dial(t).(*coapSuiteClient)
		client.register(t, watchBody(t, e.id("registered"), "#wire", e.guest("reg")))
		// The registration's reply is sent once its handler has returned,
		// and another round trip from the same source follows it.
		if r := client.call(t, capabilitiesBody(t, e)); r.result == nil {
			t.Fatalf("capabilities after registration: %+v", r)
		}
		if got := nativeObserverCount(t, e.statusClient); got != baseline {
			t.Fatalf("registration took a native lease: %d, baseline %d", got, baseline)
		}
	})
	t.Run("token_bound_to_source", func(t *testing.T) {
		// An observation token is useless from another source.
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
}
