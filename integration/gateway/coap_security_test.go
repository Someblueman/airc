//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	piondtls "github.com/pion/dtls/v3"
	"github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestCoAPSecurityRejectsWrongCertificateAndALPN(t *testing.T) {
	f := newCoAPFixture(t)
	wrongALPN := dtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(f.cert.Client.RootCAs), piondtls.WithServerName("localhost"),
		piondtls.WithSupportedProtocols("wrong-alpn"), piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
	)
	assertCoAPHandshakeRejected(t, f.address, wrongALPN, "wrong CoAPS ALPN")
	wrongName := dtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(f.cert.Client.RootCAs), piondtls.WithServerName("wrong.example"),
		piondtls.WithSupportedProtocols(coapALPN), piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
	)
	assertCoAPHandshakeRejected(t, f.address, wrongName, "wrong CoAPS certificate hostname")
	noTrust := dtls.NewDTLSClientOptions(piondtls.WithRootCAs(x509.NewCertPool()), piondtls.WithServerName("localhost"), piondtls.WithSupportedProtocols(coapALPN), piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret))
	assertCoAPHandshakeRejected(t, f.address, noTrust, "untrusted CoAPS certificate")
}

func assertCoAPHandshakeRejected(t *testing.T, address string, options dtls.DTLSClientOptions, label string) {
	t.Helper()
	conn, err := dtls.Dial(address, options)
	if err != nil {
		return
	}
	defer conn.Close()
	raw, ok := conn.NetConn().(*piondtls.Conn)
	if !ok {
		t.Fatalf("%s returned unexpected connection type %T", label, conn.NetConn())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := raw.HandshakeContext(ctx); err == nil {
		t.Fatalf("%s was accepted", label)
	}
}

func TestCoAPMalformedEnvelopeDoesNotReachNativeOrCreateJob(t *testing.T) {
	f := newCoAPFixture(t)
	before := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-malformed", Profile: "encrypted", Envelope: "not-a-jose-token"}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := response.ReadBody()
	if response.Code() != codes.BadRequest && response.Code() != codes.Unauthorized {
		t.Fatalf("malformed CoAPS status=%v body=%q", response.Code(), payload)
	}
	time.Sleep(100 * time.Millisecond)
	after := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture")
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("malformed CoAPS request changed native history: before=%v after=%v", before, after)
	}
	rpcNoJobs(t, f.config.StateDir)
	for _, profile := range []string{"signed", "encrypted"} {
		frame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-malformed-" + profile, Profile: profile, Envelope: "not-a-jose-token"}
		body, _ := json.Marshal(frame)
		response, err := f.conn.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if response.Code() != codes.BadRequest && response.Code() != codes.Unauthorized {
			t.Fatalf("invalid %s status=%v", profile, response.Code())
		}
		_, _ = response.ReadBody()
	}
	rpcNoJobs(t, f.config.StateDir)
}

func TestCoAPObserveRegistrationAloneHasNoNativeLease(t *testing.T) {
	f := newCoAPFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	request := core.Request{Version: 1, RequestID: "coaps-registration-only", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "recent", Limit: 8}
	registration := coapObserveRegister(t, f, request, true)
	if len(registration.ObservationToken) != 16 || registration.ExpiresInMS != 10000 {
		t.Fatalf("invalid registration=%+v", registration)
	}
	time.Sleep(100 * time.Millisecond)
	if got := nativeObserverCount(t, f.config.Bindings[0].Address); got != baseline {
		t.Fatalf("registration without GET created native lease: baseline=%d got=%d", baseline, got)
	}
}

func TestCoAPObservationTokenCannotTransferToFreshDTLSAssociation(t *testing.T) {
	f := newCoAPFixture(t)
	registration := coapObserveRegister(t, f, coapWatchRequest("coaps-token-owner"), true)
	token, err := hex.DecodeString(registration.ObservationToken)
	if err != nil {
		t.Fatal(err)
	}
	options := dtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(f.cert.Client.RootCAs), piondtls.WithServerName("localhost"),
		piondtls.WithSupportedProtocols(coapALPN), piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
	)
	stolen, err := dtls.Dial(f.address, options)
	if err != nil {
		t.Fatal(err)
	}
	defer stolen.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	control := core.Request{Version: 1, RequestID: "coaps-fresh-control", Operation: core.CapabilitiesGet}
	controlFrame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-fresh-control", Profile: "encrypted", Envelope: f.credentials.token(t, control, true)}
	controlBody, _ := json.Marshal(controlFrame)
	controlResponse, err := stolen.Post(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(controlBody))
	if err != nil {
		t.Fatal(err)
	}
	if controlResponse.Code() != codes.Content {
		t.Fatalf("fresh association control code=%v", controlResponse.Code())
	}
	_, _ = controlResponse.ReadBody()
	request, err := stolen.NewGetRequest(ctx, "/v1/coap/observe")
	if err != nil {
		t.Fatal(err)
	}
	request.SetToken(token)
	request.SetObserve(0)
	observation, err := stolen.DoObserve(request, func(response *pool.Message) { _, _ = response.ReadBody() })
	if err == nil || !strings.Contains(err.Error(), "BadRequest") {
		cancelCoAPSObservation(observation)
		t.Fatal("observation token transferred to a fresh DTLS association")
	}
}
