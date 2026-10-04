//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/quic-go/quic-go"
	wt "github.com/quic-go/webtransport-go"
)

func webTransportClient(f webTransportFixture, tlsConfig *tls.Config, protocols []string) *wt.Transport {
	return &wt.Transport{
		TLSClientConfig:      tlsConfig,
		ApplicationProtocols: protocols,
		QUICConfig:           &quic.Config{EnableDatagrams: true, EnableStreamResetPartialDelivery: true},
	}
}

func webTransportDialFails(t *testing.T, transport *wt.Transport, url string, headers http.Header) {
	t.Helper()
	defer transport.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	response, session, err := transport.Dial(ctx, url, headers)
	if err == nil || session != nil || (response != nil && response.StatusCode == http.StatusOK) {
		t.Fatalf("WebTransport invalid handshake succeeded: response=%v session=%v err=%v", response, session, err)
	}
}

func TestWebTransportHandshakeRejectsTLSOriginQueryAndProtocolBoundaries(t *testing.T) {
	f := newWebTransportFixture(t)
	url := "https://" + f.address + "/v1/webtransport"
	base := f.transport.TLSClientConfig.Clone()
	base.MinVersion = tls.VersionTLS13
	for _, tc := range []struct {
		name      string
		tlsConfig *tls.Config
		protocols []string
		url       string
		headers   http.Header
	}{
		{name: "wrong-ca", tlsConfig: func() *tls.Config { c := base.Clone(); c.RootCAs = nil; return c }(), protocols: []string{"airc-gateway.v1"}, url: url},
		{name: "wrong-name", tlsConfig: func() *tls.Config { c := base.Clone(); c.ServerName = "wrong.example"; return c }(), protocols: []string{"airc-gateway.v1"}, url: url},
		{name: "missing-application-protocol", tlsConfig: base.Clone(), url: url},
		{name: "wrong-application-protocol", tlsConfig: base.Clone(), protocols: []string{"wrong.protocol"}, url: url},
		{name: "malformed-application-protocol", tlsConfig: base.Clone(), protocols: []string{"airc-gateway.v1"}, url: url, headers: http.Header{"WT-Available-Protocols": []string{"["}}},
		{name: "forbidden-origin", tlsConfig: base.Clone(), protocols: []string{"airc-gateway.v1"}, url: url, headers: http.Header{"Origin": []string{"https://evil.example"}}},
		{name: "query", tlsConfig: base.Clone(), protocols: []string{"airc-gateway.v1"}, url: url + "?unexpected=1"},
		{name: "authorization", tlsConfig: base.Clone(), protocols: []string{"airc-gateway.v1"}, url: url, headers: http.Header{"Authorization": []string{"Bearer forbidden-on-connect"}}},
		{name: "idempotency", tlsConfig: base.Clone(), protocols: []string{"airc-gateway.v1"}, url: url, headers: http.Header{"Idempotency-Key": []string{"forbidden-on-connect"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			webTransportDialFails(t, webTransportClient(f, tc.tlsConfig, tc.protocols), tc.url, tc.headers)
		})
	}
}

func TestWebTransportTamperedEnvelopeDoesNotCreateNativeWork(t *testing.T) {
	f := newWebTransportFixture(t)
	baseline := nativeObserverCount(t, f.config.Bindings[0].Address)
	stream, err := f.session.OpenStreamSync(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	request := core.Request{Version: 1, RequestID: "webtransport-tampered", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not post"}
	envelope := f.credentials.token(t, request, false)
	// Keep the outer frame valid while changing a byte inside the signed token.
	// This exercises authentication failure rather than malformed JSON framing.
	tampered := []byte(envelope)
	if tampered[len(tampered)/2] == 'A' {
		tampered[len(tampered)/2] = 'B'
	} else {
		tampered[len(tampered)/2] = 'A'
	}
	frame := webTransportFrame(t, adapter.FrameRequest{Version: 1, CorrelationID: "wt-tampered", Profile: "signed", Envelope: string(tampered)})
	if _, err := stream.Write(frame); err != nil {
		t.Fatal(err)
	}
	if err := stream.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var header [4]byte
	if _, err := io.ReadFull(stream, header[:]); err != nil {
		t.Fatalf("tampered envelope did not produce typed authentication failure: %v", err)
	}
	size := binary.BigEndian.Uint32(header[:])
	if size == 0 || size > adapter.MaxResponseFrameBytes {
		t.Fatalf("invalid rejection response size=%d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(stream, body); err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(body, &response); err != nil || response.Error == nil || response.Error.Code != core.AuthenticationFailed || response.Result != nil {
		t.Fatalf("tampered envelope response=%+v err=%v", response, err)
	}
	waitNativeObserverCountAtMost(t, f.config.Bindings[0].Address, baseline)
}
