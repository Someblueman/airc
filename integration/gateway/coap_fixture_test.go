//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	piondtls "github.com/pion/dtls/v3"
	coapdtls "github.com/plgd-dev/go-coap/v3/dtls"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapNetClient "github.com/plgd-dev/go-coap/v3/net/client"
	coapOptions "github.com/plgd-dev/go-coap/v3/options"
	coapUDP "github.com/plgd-dev/go-coap/v3/udp"
	coapClient "github.com/plgd-dev/go-coap/v3/udp/client"
)

const coapALPN = "coap"

type coapFixture struct {
	process     *gatewayProcess
	address     string
	config      config.Config
	credentials *detachedCredentials
	conn        *coapClient.Conn
	cert        testtls.Certificate
	observer    *irc.Client
}

func newCoAPFixture(t *testing.T) coapFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	promoteRoomOperator(t, native)
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	credentials := provisionDetached(t, &c)
	cert := testtls.NewP256(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "coaps", Protocol: "coaps", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := mcpGatewayAddress(t, p)
	if _, err := net.ResolveUDPAddr("udp", address); err != nil {
		t.Fatal(err)
	}
	conn := dialCoAPS(t, address, cert)
	observer := dial(t, native, "coaps-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "coaps-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return coapFixture{process: p, address: address, config: c, credentials: credentials, conn: conn, cert: cert, observer: observer}
}

func dialCoAPS(t *testing.T, address string, cert testtls.Certificate) *coapClient.Conn {
	return dialCoAPSWithOptions(t, address, cert)
}

func dialCoAPSWithOptions(t *testing.T, address string, cert testtls.Certificate, extra ...coapUDP.Option) *coapClient.Conn {
	t.Helper()
	dtlsOptions := coapdtls.NewDTLSClientOptions(
		piondtls.WithRootCAs(cert.Client.RootCAs),
		piondtls.WithServerName("localhost"),
		piondtls.WithSupportedProtocols(coapALPN),
		piondtls.WithExtendedMasterSecret(piondtls.RequireExtendedMasterSecret),
		piondtls.WithCipherSuites(piondtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, piondtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384),
		piondtls.WithMTU(1200),
	)
	coapOpts := []coapUDP.Option{coapOptions.WithBlockwise(true, blockwise.SZX1024, 5*time.Second)}
	coapOpts = append(coapOpts, extra...)
	conn, err := coapdtls.Dial(address, dtlsOptions, coapOpts...)
	if err != nil {
		t.Fatal(err)
	}
	dtlsConn, ok := conn.NetConn().(*piondtls.Conn)
	if !ok {
		_ = conn.Close()
		t.Fatalf("CoAPS connection is %T, want *dtls.Conn", conn.NetConn())
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if err := dtlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		t.Fatalf("CoAPS DTLS handshake: %v", err)
	}
	state, stateOK := dtlsConn.ConnectionState()
	if !stateOK || state.NegotiatedProtocol != coapALPN || len(state.PeerCertificates) == 0 {
		_ = conn.Close()
		t.Fatalf("CoAPS authenticated state=%+v ok=%v", state, stateOK)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func cancelCoAPSObservation(observation coapNetClient.Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = observation.Cancel(ctx)
}

func coapRPC(t *testing.T, f coapFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	return coapRPCType(t, f, request, encrypted, message.Confirmable)
}

func coapRPCType(t *testing.T, f coapFixture, request core.Request, encrypted bool, typ message.Type) adapter.FrameResponse {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: "coaps-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	req, err := f.conn.NewPostRequest(ctx, "/v1/coap/rpc", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.SetType(typ)
	response, err := f.conn.Do(req)
	f.conn.ReleaseMessage(req)
	if err != nil {
		t.Fatalf("CoAPS operation=%s request_id=%s: %v", request.Operation, request.RequestID, err)
	}
	payload, err := response.ReadBody()
	if err != nil {
		f.conn.ReleaseMessage(response)
		t.Fatal(err)
	}
	if response.Code() != codes.Content {
		f.conn.ReleaseMessage(response)
		t.Fatalf("CoAPS operation=%s request_id=%s code=%v payload=%q", request.Operation, request.RequestID, response.Code(), payload)
	}
	f.conn.ReleaseMessage(response)
	var result adapter.FrameResponse
	if err := json.Unmarshal(payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Version != 1 || result.CorrelationID != frame.CorrelationID || result.Event != "result" || result.Sequence != 1 || result.Result == nil || result.Result.Operation != request.Operation {
		t.Fatalf("CoAPS operation=%s request_id=%s response=%+v error=%+v", request.Operation, request.RequestID, result, result.Error)
	}
	return result
}

type coapRegistration struct {
	Version          int    `json:"version"`
	CorrelationID    string `json:"correlation_id"`
	ObservationToken string `json:"observation_token"`
	ExpiresInMS      int    `json:"expires_in_ms"`
}

func coapObserveRegister(t *testing.T, f coapFixture, request core.Request, encrypted bool) coapRegistration {
	t.Helper()
	frame := frameRequest{Version: 1, CorrelationID: "coaps-observe-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted), Stream: true}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	response, err := f.conn.Post(ctx, "/v1/coap/observe/register", message.AppJSON, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	payload, err := response.ReadBody()
	if err != nil {
		t.Fatal(err)
	}
	var registration coapRegistration
	if response.Code() != codes.Content || json.Unmarshal(payload, &registration) != nil || registration.Version != 1 || registration.CorrelationID != frame.CorrelationID || len(registration.ObservationToken) != 16 || registration.ExpiresInMS != 10000 {
		t.Fatalf("CoAPS observe registration code=%v payload=%q registration=%+v", response.Code(), payload, registration)
	}
	if _, err := hex.DecodeString(registration.ObservationToken); err != nil {
		t.Fatalf("CoAPS observation token=%q: %v", registration.ObservationToken, err)
	}
	return registration
}

func coapWatchRequest(id string) core.Request {
	return core.Request{Version: 1, RequestID: id, Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
}
