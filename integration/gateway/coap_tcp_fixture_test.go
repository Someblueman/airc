//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	coapNet "github.com/plgd-dev/go-coap/v3/net"
	"github.com/plgd-dev/go-coap/v3/net/blockwise"
	coapNetClient "github.com/plgd-dev/go-coap/v3/net/client"
	coapClient "github.com/plgd-dev/go-coap/v3/tcp/client"
)

const coapTCPALPN = "coap"

type coapTCPFixture struct {
	process           *gatewayProcess
	address           string
	config            config.Config
	credentials       *detachedCredentials
	conn              *coapClient.Conn
	cert              testtls.Certificate
	observer          *irc.Client
	responseSigning   ed25519.PublicKey
	responseRecipient *ecdsa.PrivateKey
}

func newCoAPTCPFixture(t *testing.T) coapTCPFixture { return newCoAPTCPProfileFixture(t, false) }

func newCoAPTCPProfileFixture(t *testing.T, plain bool) coapTCPFixture {
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
	c.Principals[0].Operations = ops
	c.Principals[0].Targets = []core.Target{{Kind: "room", Name: "#gateway-fixture"}, {Kind: "direct", Name: "fixture-author"}}
	credentials := provisionDetached(t, &c)
	var cert testtls.Certificate
	var responseSigning ed25519.PublicKey
	var responseRecipient *ecdsa.PrivateKey
	c.Listeners = []config.Listener{{ID: "coap-tcp", Protocol: "coap_tcp", Enabled: true, Address: "127.0.0.1:0", Operations: ops}}
	if plain {
		responseSigning, responseRecipient = provisionUDPResponseCredentials(t, &c)
		c.ReturnRoutes = []config.ReturnRoute{{ID: "coap-tcp-return", Principal: c.Principals[0].ID, Protocol: "coap_tcp", Listener: "coap-tcp", Destination: "connection", RecipientKeyID: "udp-reader", RecipientPublicKeyFile: filepath.Join(filepath.Dir(c.StateDir), "udp-reader-public.pem")}}
	} else {
		cert = testtls.NewP256(t)
		if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
			t.Fatal(err)
		}
		c.Listeners[0].ID = "coaps-tcp"
		c.Listeners[0].Protocol = "coaps_tcp"
		c.Listeners[0].TLSCertFile, c.Listeners[0].TLSKeyFile = cert.CertPath, cert.KeyPath
	}

	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := mcpGatewayAddress(t, p)
	if _, err := net.ResolveTCPAddr("tcp", address); err != nil {
		t.Fatal(err)
	}
	var conn *coapClient.Conn
	if plain {
		conn = dialPlainCoAPTCP(t, address)
	} else {
		conn = dialCoAPTCP(t, address, cert)
	}
	observer := dial(t, native, "coaps-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "coaps-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return coapTCPFixture{process: p, address: address, config: c, credentials: credentials, conn: conn, cert: cert, observer: observer, responseSigning: responseSigning, responseRecipient: responseRecipient}
}

func dialCoAPTCP(t *testing.T, address string, cert testtls.Certificate) *coapClient.Conn {
	return dialCoAPTCPTransport(t, address, cert, func(*coapClient.Conn, *pool.Message) (bool, error) { return false, nil })
}

func dialCoAPTCPWithResponseDrop(t *testing.T, address string, cert testtls.Certificate, dropped chan<- struct{}) *coapClient.Conn {
	var once atomic.Bool
	return dialCoAPTCPTransport(t, address, cert, func(_ *coapClient.Conn, req *pool.Message) (bool, error) {
		if req.Code() == codes.Content && once.CompareAndSwap(false, true) {
			select {
			case dropped <- struct{}{}:
			default:
			}
			return true, nil
		}
		return false, nil
	})
}

func dialCoAPTCPTransport(t *testing.T, address string, cert testtls.Certificate, monitor coapClient.RequestMonitorFunc) *coapClient.Conn {
	return dialCoAPTCPWithLimit(t, address, cert, 1152, monitor)
}

// The client decoder enforces the advertised receive limit on actual wire frames.
func dialCoAPTCPWithLimit(t *testing.T, address string, cert testtls.Certificate, maximum uint32, monitor coapClient.RequestMonitorFunc) *coapClient.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	tlsConfig := &tls.Config{RootCAs: cert.Client.RootCAs, ServerName: "localhost", MinVersion: tls.VersionTLS13, NextProtos: []string{coapTCPALPN}}
	raw, err := (&tls.Dialer{NetDialer: &net.Dialer{Timeout: 3 * time.Second}, Config: tlsConfig}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	state := raw.(*tls.Conn).ConnectionState()
	if state.Version != tls.VersionTLS13 || state.NegotiatedProtocol != coapTCPALPN || len(state.VerifiedChains) == 0 {
		_ = raw.Close()
		t.Fatalf("verified CoAP TLS state=%+v", state)
	}
	return startCoAPTCPClient(t, ctx, raw, maximum, monitor)
}

func dialPlainCoAPTCP(t *testing.T, address string) *coapClient.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	raw, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	return startCoAPTCPClient(t, ctx, raw, 1152, func(*coapClient.Conn, *pool.Message) (bool, error) { return false, nil })
}

func startCoAPTCPClient(t *testing.T, ctx context.Context, raw net.Conn, maximum uint32, monitor coapClient.RequestMonitorFunc) *coapClient.Conn {
	t.Helper()
	var err error
	cfg := coapClient.DefaultConfig
	cfg.DisableTCPSignalMessageCSM = true
	cfg.CloseSocket = true
	cfg.MaxMessageSize = maximum
	cfg.BlockwiseSZX = blockwise.SZX1024
	cfg.Errors = func(error) {}
	conn := coapClient.NewConnWithOpts(coapNet.NewConn(raw), &cfg,
		coapClient.WithRequestMonitor(monitor),
		coapClient.WithBlockWise(func(c *coapClient.Conn) *blockwise.BlockWise[*coapClient.Conn] {
			return blockwise.New(c, 3*time.Second, func(error) {}, c.GetObservationRequest)
		}),
	)
	csm := conn.AcquireMessage(ctx)
	csm.SetCode(codes.CSM)
	csm.SetOptionUint32(message.TCPMaxMessageSize, maximum)
	csm.SetOptionBytes(message.TCPBlockWiseTransfer, nil)
	_ = raw.SetWriteDeadline(time.Now().Add(3 * time.Second))
	err = conn.Session().WriteMessage(csm)
	conn.ReleaseMessage(csm)
	_ = raw.SetWriteDeadline(time.Time{})
	if err != nil {
		_ = conn.Close()
		_ = conn.Run()
		t.Fatal(err)
	}
	// Send the initiator CSM before waiting for the acceptor CSM, as RFC8323
	// requires. The pinned library's default CSM omits blockwise capabilities.
	received := make(chan struct{})
	var once sync.Once
	conn.SetTCPSignalReceivedHandler(func(code codes.Code) {
		if code == codes.CSM {
			once.Do(func() { close(received) })
		}
	})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = conn.Run() }()
	t.Cleanup(func() {
		_ = conn.Close()
		select {
		case <-runDone:
		case <-time.After(3 * time.Second):
			t.Error("CoAP TCP fixture reader did not join")
		}
	})
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal("server CSM not received")
	case <-runDone:
		t.Fatal("carrier closed before server CSM")
	}
	return conn
}

func cancelCoAPSTCPObservation(observation coapNetClient.Observation) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = observation.Cancel(ctx)
}

func coapTCPRPC(t *testing.T, f coapTCPFixture, request core.Request, encrypted bool) adapter.FrameResponse {
	t.Helper()
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-" + request.RequestID, Profile: map[bool]string{false: "signed", true: "encrypted"}[encrypted], Envelope: f.credentials.token(t, request, encrypted)}
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
	response, err := f.conn.Do(req)
	f.conn.ReleaseMessage(req)
	if err != nil {
		t.Fatalf("CoAPS TCP operation=%s request_id=%s: %v", request.Operation, request.RequestID, err)
	}
	payload, err := response.ReadBody()
	if err != nil {
		f.conn.ReleaseMessage(response)
		t.Fatal(err)
	}
	code := response.Code()
	if code != codes.Content {
		f.conn.ReleaseMessage(response)
		t.Fatalf("CoAPS operation=%s request_id=%s code=%v payload=%q", request.Operation, request.RequestID, code, payload)
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

func coapTCPAccepted(t *testing.T, f coapTCPFixture, request core.Request, encrypted bool) core.Result {
	t.Helper()
	first := coapTCPRPC(t, f, request, encrypted)
	if first.Result == nil || first.Result.Result == nil || !first.Result.Result.Durable {
		t.Fatalf("CoAPS admission=%+v", first)
	}
	last := *first.Result.Result
	// Receipt polling has its own bounded association so delivery delays cannot
	// consume the operation-matrix peer's 256-token lifetime admission budget.
	receipts := f
	receipts.conn = dialCoAPTCP(t, f.address, f.cert)
	defer receipts.conn.Close()
	deadline := time.Now().Add(8 * time.Second)
	for attempt := 0; time.Now().Before(deadline); attempt++ {
		receipt := core.Request{Version: 1, RequestID: fmt.Sprintf("%s-receipt-%d", request.RequestID, attempt), Operation: core.ReceiptGet, LookupID: request.RequestID}
		response := coapTCPRPC(t, receipts, receipt, encrypted)
		if response.Result != nil && response.Result.Result != nil {
			last = *response.Result.Result
			if last.State == core.Accepted {
				return last
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
	t.Fatalf("CoAPS request did not reach accepted: %+v", last)
	return core.Result{}
}

type coapTCPRegistration struct {
	Version          int    `json:"version"`
	CorrelationID    string `json:"correlation_id"`
	ObservationToken string `json:"observation_token"`
	ExpiresInMS      int    `json:"expires_in_ms"`
}

func coapTCPObserveRegister(t *testing.T, f coapTCPFixture, request core.Request, encrypted bool) coapTCPRegistration {
	t.Helper()
	frame := adapter.FrameRequest{Version: 1, CorrelationID: "coaps-observe-" + request.RequestID, Profile: map[bool]string{false: "signed", true: "encrypted"}[encrypted], Envelope: f.credentials.token(t, request, encrypted), Stream: true}
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
	defer f.conn.ReleaseMessage(response)
	payload, err := response.ReadBody()
	if err != nil {
		t.Fatal(err)
	}
	var registration coapTCPRegistration
	code := response.Code()
	if code != codes.Content || json.Unmarshal(payload, &registration) != nil || registration.Version != 1 || registration.CorrelationID != frame.CorrelationID || len(registration.ObservationToken) != 16 || registration.ExpiresInMS != 10000 {
		t.Fatalf("CoAPS observe registration code=%v payload=%q registration=%+v", code, payload, registration)
	}
	if _, err := hex.DecodeString(registration.ObservationToken); err != nil {
		t.Fatalf("CoAPS observation token=%q: %v", registration.ObservationToken, err)
	}
	return registration
}
