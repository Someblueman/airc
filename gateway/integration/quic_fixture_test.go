//go:build darwin || linux

package gateway_test

import (
	"context"
	cryptotls "crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go"
)

const rawQUICALPN = "airc-gateway-quic/1"

type rawQUICFixture struct {
	process     *gatewayProcess
	address     string
	token       string
	config      config.Config
	credentials *detachedCredentials
	conn        *quic.Conn
	cert        testtls.Certificate
	observer    *irc.Client
}

func newRawQUICFixture(t *testing.T, datagrams bool) rawQUICFixture {
	return newRawQUICFixtureWithConfig(t, datagrams, nil)
}

func newRawQUICFixtureWithConfig(t *testing.T, datagrams bool, clientConfig *quic.Config) rawQUICFixture {
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
	cert := testtls.New(t)
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	protocol := "rawquic"
	if datagrams {
		protocol = "rawquic_datagrams"
	}
	c.Listeners = []config.Listener{{ID: "raw-quic", Protocol: protocol, Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, Operations: ops, Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	registerGatewayShutdownCheck(t, p)
	address := mcpGatewayAddress(t, p)
	token := fixtureAuthor + ":" + fixtureAccount
	tlsConfig := cert.Client.Clone()
	tlsConfig.MinVersion = cryptotls.VersionTLS13
	tlsConfig.NextProtos = []string{rawQUICALPN}
	quicConfig := clientConfig
	if quicConfig == nil {
		quicConfig = &quic.Config{}
	} else {
		quicConfig = quicConfig.Clone()
	}
	quicConfig.EnableDatagrams = datagrams
	quicConfig.EnableStreamResetPartialDelivery = true
	quicConfig.Allow0RTT = false
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := quic.DialAddr(ctx, address, tlsConfig, quicConfig)
	if err != nil {
		t.Fatal(err)
	}
	state := conn.ConnectionState()
	if state.TLS.Version != cryptotls.VersionTLS13 || state.TLS.NegotiatedProtocol != rawQUICALPN {
		_ = conn.CloseWithError(0, "bad test handshake")
		t.Fatalf("raw QUIC TLS/ALPN=%x/%q", state.TLS.Version, state.TLS.NegotiatedProtocol)
	}
	if datagrams && (!state.SupportsDatagrams.Local || !state.SupportsDatagrams.Remote) {
		_ = conn.CloseWithError(0, "datagrams not negotiated")
		t.Fatal("raw QUIC datagrams were not negotiated")
	}
	t.Cleanup(func() { _ = conn.CloseWithError(0, "test complete") })
	observer := dial(t, native, "raw-quic-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "raw-quic-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	return rawQUICFixture{process: p, address: address, token: token, config: c, credentials: credentials, conn: conn, cert: cert, observer: observer}
}

func rawQUICFrame(t *testing.T, request frameRequest) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("marshal raw QUIC frame: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

func readRawQUICFrame(t *testing.T, reader io.Reader) adapter.FrameResponse {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		t.Fatal(err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatalf("invalid raw QUIC response size=%d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(reader, body); err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func rawQUICRequest(t *testing.T, f rawQUICFixture, request core.Request, encrypted bool, stream bool) frameRequest {
	t.Helper()
	return frameRequest{Version: 1, CorrelationID: "quic-" + request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, encrypted), Stream: stream}
}

func waitNativeLeaseGone(t *testing.T, statusClient *irc.Client, baseline int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if nativeObserverCount(t, statusClient) <= baseline {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("gateway watch lease remained: baseline=%d current=%d", baseline, nativeObserverCount(t, statusClient))
}
