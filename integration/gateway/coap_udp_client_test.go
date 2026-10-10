//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

type coapMailboxReader struct {
	*net.UDPConn
	t *testing.T
}

func (c coapMailboxReader) Read(dst []byte) (int, error) {
	wire := make([]byte, 1201)
	n, err := c.UDPConn.Read(wire)
	if err != nil {
		return 0, err
	}
	req := pool.NewMessage(context.Background())
	consumed, err := req.UnmarshalWithDecoder(coder.DefaultCoder, wire[:n])
	path, pathErr := req.Path()
	format, formatErr := req.ContentFormat()
	if n > 1200 || err != nil || consumed != n || req.Type() != message.NonConfirmable || req.Code() != codes.POST || pathErr != nil || path != "/v1/coap/mailbox" || formatErr != nil || format != message.AppOctets {
		c.t.Fatal("invalid CoAP mailbox datagram", req, err)
	}
	payload, err := req.ReadBody()
	if err != nil || len(payload) > len(dst) {
		c.t.Fatal("invalid CoAP mailbox payload", err, len(payload))
	}
	return copy(dst, payload), nil
}

func coapUDPWriteFrame(t *testing.T, client *coapClient.Conn, frame adapter.FrameRequest) {
	t.Helper()
	body, err := json.Marshal(frame)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatal("CoAP request size", len(body), err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for _, fragment := range webTransportDatagrams(body, id) {
		req, err := client.NewPostRequest(ctx, "/v1/coap/fragments", message.AppOctets, bytes.NewReader(fragment))
		if err != nil {
			t.Fatal(err)
		}
		req.SetType(message.NonConfirmable)
		err = client.WriteMessage(req)
		client.ReleaseMessage(req)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func coapUDPCall(t *testing.T, f udpFixture, client *coapClient.Conn, request core.Request) adapter.FrameResponse {
	t.Helper()
	correlation := "coap-" + request.RequestID
	coapUDPWriteFrame(t, client, adapter.FrameRequest{Version: 1, CorrelationID: correlation, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	response := coapUDPReadResponse(t, f, time.Now().Add(5*time.Second))
	if response.Version != 1 || response.CorrelationID != correlation || response.Event != "result" || response.Sequence != 1 || response.Error != nil || response.Result == nil || response.Result.Operation != request.Operation {
		t.Fatal("CoAP correlated response", response)
	}
	return response
}

func coapUDPReadResponse(t *testing.T, f udpFixture, deadline time.Time) adapter.FrameResponse {
	t.Helper()

	token := string(readAGD1Payload(t, coapMailboxReader{f.mailbox, t}, deadline))
	plain, err := auth.OpenResponse(token, "udp-response", f.responseSigning, "udp-reader", f.responseRecipient, auth.ResponseScope{Realm: f.config.Realm, Principal: f.config.Principals[0].ID, Binding: f.config.Principals[0].Binding, Route: "udp-return"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func coapUDPClient(t *testing.T, f udpFixture) *coapClient.Conn {
	t.Helper()
	client, err := coapUDP.Dial(f.address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func coapUDPAccepted(t *testing.T, f udpFixture, client *coapClient.Conn, request core.Request) core.Result {
	t.Helper()
	initial := coapUDPCall(t, f, client, request)
	if initial.Result.Result == nil || !initial.Result.Result.Durable || initial.Result.Result.RequestID != request.RequestID {
		t.Fatal("CoAP durable admission", initial)
	}
	deadline := time.Now().Add(5 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := coapUDPCall(t, f, client, core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID})
		if receipt.Result.Result == nil || receipt.Result.Result.RequestID != request.RequestID {
			t.Fatal("CoAP receipt", receipt)
		}
		if receipt.Result.Result.State == core.Accepted {
			return *receipt.Result.Result
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatal("CoAP send did not reach accepted receipt")
	return core.Result{}
}
