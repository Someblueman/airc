//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/dtls/v3"
)

func TestDTLSCertificateAndALPNFailuresHaveNoNativeIntake(t *testing.T) {
	f := newDTLSFixture(t)
	remote, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hostname", "trust", "alpn"} {
		t.Run(name, func(t *testing.T) {
			roots, host, alpn := f.cert.Client.RootCAs, "localhost", dtlsALPN
			switch name {
			case "hostname":
				host = "wrong.example"
			case "trust":
				roots = x509.NewCertPool()
			case "alpn":
				alpn = "wrong-protocol"
			}
			conn, err := dtls.DialWithOptions("udp", remote, dtls.WithRootCAs(roots), dtls.WithServerName(host), dtls.WithSupportedProtocols(alpn), dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret), dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = conn.HandshakeContext(ctx)
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s handshake did not reject its identity/protocol: %v", name, err)
			}
			if name == "hostname" {
				var mismatch x509.HostnameError
				if !errors.As(err, &mismatch) {
					t.Fatalf("hostname error=%v", err)
				}
			}
			if name == "trust" {
				var untrusted x509.UnknownAuthorityError
				if !errors.As(err, &untrusted) {
					t.Fatalf("trust error=%v", err)
				}
			}
		})
	}
	rpcNoJobs(t, f.config.StateDir)
	if got := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(got) != 0 {
		t.Fatalf("failed DTLS handshakes posted=%+v", got)
	}
}

func TestDTLSInvalidFramesHaveNoNativeIntake(t *testing.T) {
	for _, invalid := range []string{"unknown-profile", "malformed", "oversize-record"} {
		t.Run(invalid, func(t *testing.T) {
			f := newDTLSFixture(t)
			request := core.Request{Version: 1, RequestID: "dtls-invalid", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never post"}
			frame := frameRequest{Version: 1, CorrelationID: "dtls-invalid", Profile: "plain", Request: f.credentials.token(t, request, true)}
			switch invalid {
			case "unknown-profile":
				frame.Profile = "unknown"
				writeDTLSFrame(t, f, frame)
			case "malformed":
				for _, gram := range webTransportDatagrams([]byte(`{"version":1,"unknown":true}`), [16]byte{1}) {
					if _, err := f.conn.Write(gram); err != nil {
						t.Fatal(err)
					}
				}
			case "oversize-record":
				if _, err := f.conn.Write(make([]byte, 1001)); err != nil {
					t.Fatal(err)
				}
			}
			if invalid == "unknown-profile" {
				// A well-formed frame with an unknown profile gets a correlated
				// refusal and the association stays usable.
				response := readDTLSResponse(t, f.conn, time.Now().Add(time.Second))
				if response.Event != "error" || response.Error == nil || response.Error.Code != core.InvalidRequest {
					t.Fatalf("unknown profile response=%+v", response)
				}
				dtlsDatagramCall(t, f, core.Request{Version: 1, RequestID: "dtls-after-unknown-profile", Operation: core.CapabilitiesGet}, true)
			} else {
				_ = f.conn.SetReadDeadline(time.Now().Add(time.Second))
				if n, err := f.conn.Read(make([]byte, 1201)); err == nil || n != 0 {
					t.Fatalf("malformed DTLS input produced response bytes=%d err=%v", n, err)
				}
			}
			rpcNoJobs(t, f.config.StateDir)
			if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
				t.Fatalf("invalid DTLS packet posted=%+v", messages)
			}
		})
	}
}
