//go:build darwin || linux

package gateway_test

import (
	"context"
	cryptotls "crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/quic-go/quic-go"
	wt "github.com/quic-go/webtransport-go"
)

type webTransportFixture struct {
	process     *gatewayProcess
	address     string
	token       string
	config      config.Config
	credentials *detachedCredentials
	transport   *wt.Transport
	session     *wt.Session
	observer    *irc.Client
}

func newWebTransportFixture(t *testing.T) webTransportFixture {
	return newWebTransportFixtureWith(t, nil, nil, nil)
}

// newWebTransportFixtureWith allows boundary/browser tests to share the same
// pinned native fixture while choosing the listener origin policy, certificate,
// and client QUIC flow-control settings before the gateway starts.
func newWebTransportFixtureWith(t *testing.T, origins []string, certificate *testtls.Certificate, quicConfig *quic.Config) webTransportFixture {
	t.Helper()
	native := fixtureWithAdmin(t, true)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	provision := dial(t, native, "fixture-author", account, true, true)
	_ = provision.Close()
	c, _ := acceptanceJournalFixture(t, native, account)
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	ops := dispatch.ImplementedOperations()
	credentials := provisionDetached(t, &c)
	cert := testtls.New(t)
	if certificate != nil {
		cert = *certificate
	}
	if _, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath); err != nil {
		t.Fatal(err)
	}
	c.Listeners = []config.Listener{{ID: "webtransport", Protocol: "webtransport", Enabled: true, Address: "127.0.0.1:0", TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, AllowedOrigins: origins, Operations: ops, Limits: fixtureListenerLimits()}}
	p := startGateway(t, gatewayBinary(t), c)
	// Preserve a useful goroutine dump if the owned gateway cannot join its
	// shutdown. Client/observer cleanups registered below run before this one.
	t.Cleanup(func() {
		select {
		case <-p.done:
			if p.err != nil {
				t.Errorf("WebTransport gateway exited: %v stderr=%s", p.err, p.stderr.String())
			}
			return
		default:
		}
		_ = p.command.Process.Signal(os.Interrupt)
		select {
		case <-p.done:
			if p.err != nil {
				t.Errorf("WebTransport gateway shutdown: %v stderr=%s", p.err, p.stderr.String())
			}
		case <-time.After(7 * time.Second):
			_ = p.command.Process.Signal(syscall.SIGQUIT)
			select {
			case <-p.done:
			case <-time.After(time.Second):
				_ = p.command.Process.Kill()
				<-p.done
			}
			t.Errorf("WebTransport shutdown did not join: %s", p.stderr.String())
		}
	})
	// The binding joins after gateway startup; grant its native nick operator
	// status once more so room.update exercises the real permission path.
	promoteRoomOperator(t, native)
	address := mcpGatewayAddress(t, p)
	tlsConfig := cert.Client.Clone()
	tlsConfig.MinVersion = cryptotls.VersionTLS13
	tlsConfig.NextProtos = []string{"h3"}
	if quicConfig == nil {
		quicConfig = &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true}
	} else {
		quicConfig = quicConfig.Clone()
		quicConfig.EnableDatagrams = true
		quicConfig.EnableStreamResetPartialDelivery = true
	}
	transport := &wt.Transport{TLSClientConfig: tlsConfig, ApplicationProtocols: []string{"airc-gateway.v1"}, QUICConfig: quicConfig, Config: &wt.Config{MaxIncomingStreams: 4, MaxIncomingUniStreams: 4, MaxIncomingData: 64 << 10}}
	t.Cleanup(func() { _ = transport.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	response, session, err := transport.Dial(ctx, "https://"+address+"/v1/webtransport", nil)
	if err != nil {
		t.Fatal(err)
	}
	state := session.SessionState()
	if response.StatusCode != 200 || state.ApplicationProtocol != "airc-gateway.v1" || state.ConnectionState.TLS.Version != cryptotls.VersionTLS13 || state.ConnectionState.TLS.NegotiatedProtocol != "h3" {
		t.Fatalf("WebTransport handshake status/tls/alpn=%d/%v/%q", response.StatusCode, state.ConnectionState.TLS, state.ApplicationProtocol)
	}
	t.Cleanup(func() { _ = session.CloseWithError(0, "test complete") })
	observer := dial(t, native, "webtransport-observer", "", false, false)
	if err := observer.Join("#gateway-fixture"); err != nil {
		t.Fatal(err)
	}
	next(t, observer, func(e *irc.JoinEvent) bool { return e.Agent == "webtransport-observer" })
	t.Cleanup(func() { _ = observer.Close() })
	token := fixtureAuthor + ":" + fixtureAccount
	return webTransportFixture{process: p, address: address, token: token, config: c, credentials: credentials, transport: transport, session: session, observer: observer}
}

func webTransportFrame(t *testing.T, request frameRequest) []byte {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil || len(body) > adapter.MaxRequestFrameBytes {
		t.Fatalf("marshal WebTransport frame: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

func readWebTransportFrame(t *testing.T, reader io.Reader) adapter.FrameResponse {
	t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		t.Fatal(err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatalf("invalid WebTransport response size=%d", size)
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
