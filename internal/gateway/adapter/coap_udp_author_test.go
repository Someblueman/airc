package adapter

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapudp "github.com/plgd-dev/go-coap/v3/udp"
)

// An Observe watch over plain UDP CoAP runs once its ready marker is on the
// wire: the next notification is an AGD1 fragment of the watch's first frame.
// The fixture's AIRC is never reachable, so that frame is the watch's
// correlated failure; what matters is that the watch ran at all instead of
// waiting on a ready gate that only a write hook could open.
func TestServeCoAPUDPObserveRunsAfterReadyMarker(t *testing.T) {
	f := newRouteFixture(t)
	d := f.listener(t, config.Listener{ID: "coap-udp", Protocol: "coap_udp", Targets: []core.Target{{Kind: "room", Name: "#fixture"}}})
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	served := make(chan error, 1)
	go func() { served <- ServeCoAPUDP(ctx, conn, d, make(chan struct{}, 4), make(chan struct{}, 8)) }()
	defer func() {
		cancel()
		if err := <-served; err != nil {
			t.Error(err)
		}
	}()
	client, err := coapudp.Dial(conn.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	request := []byte(`{"version":1,"request_id":"udp-observe","operation":"watch","target":{"kind":"room","name":"#fixture"},"start":"retained","limit":8,"author":{"nick":"udp-watcher"}}`)
	frame := []byte(`{"version":1,"correlation_id":"watch","profile":"plain","stream":true,"request":` + string(request) + `}`)
	exchange, done := context.WithTimeout(t.Context(), 5*time.Second)
	defer done()
	response, err := client.Post(exchange, "/v1/coap/observe/register", message.AppJSON, bytes.NewReader(frame))
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

	observe, err := client.NewGetRequest(exchange, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	observe.SetToken(token)
	observe.SetObserve(0)
	type note struct {
		sequence uint32
		format   message.MediaType
		body     []byte
	}
	notes := make(chan note, 8)
	observation, err := client.DoObserve(observe, func(n *pool.Message) {
		body, _ := n.ReadBody()
		sequence, _ := n.Observe()
		format, _ := n.ContentFormat()
		select {
		case notes <- note{sequence, format, append([]byte(nil), body...)}:
		default:
		}
	})
	client.ReleaseMessage(observe)
	if err != nil {
		t.Fatal(err)
	}
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
