//go:build darwin || linux

package gateway_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/pion/dtls/v3"
)

func dialDTLSIdlePeer(f dtlsFixture, timeout time.Duration) (*dtls.Conn, error) {
	address, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	udp, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	conn, err := dtls.ClientWithOptions(udp, address,
		dtls.WithRootCAs(f.cert.Client.RootCAs),
		dtls.WithServerName("localhost"),
		dtls.WithSupportedProtocols(dtlsALPN),
		dtls.WithExtendedMasterSecret(dtls.RequireExtendedMasterSecret),
		dtls.WithCipherSuites(dtls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256, dtls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384),
		dtls.WithMTU(1200),
	)
	if err != nil {
		_ = udp.Close()
		return nil, err
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func tamperDTLSSignature(t *testing.T, token string) string {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("compact signed token segments=%d", len(parts))
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(signature) == 0 {
		t.Fatalf("decode signed token: %v", err)
	}
	signature[0] ^= 1
	parts[2] = base64.RawURLEncoding.EncodeToString(signature)
	return strings.Join(parts, ".")
}

// A DTLS association must not be kept alive by records that the gateway
// cannot authenticate or parse. This uses one real owner process so the
// packet path, DTLS worker, and idle reaper are all exercised together.
func TestDTLSMalformedAndUnauthorizedRecordsDoNotRenewIdle(t *testing.T) {
	f := newDTLSFixtureWithSessionLimit(t, 2)
	started := time.Now()
	second, err := dialDTLSIdlePeer(f, 5*time.Second)
	if err != nil {
		t.Fatalf("second DTLS association: %v", err)
	}
	defer second.Close()
	blocked, err := dialDTLSIdlePeer(f, 400*time.Millisecond)
	if err == nil {
		blocked.Close()
		t.Fatal("third DTLS association bypassed the configured session bound")
	}

	// Repeat both classes of bad application payload while the association is
	// otherwise quiet. Neither is allowed to move the authenticated idle clock.
	secondFixture := f
	secondFixture.conn = second
	validRenewed := false
	for i := 0; time.Since(started) < 28*time.Second; i++ {
		if _, err := f.conn.Write([]byte("AGD1")); err != nil {
			t.Fatalf("malformed DTLS application record %d: %v", i, err)
		}
		request := core.Request{Version: 1, RequestID: fmt.Sprintf("dtls-idle-unauthorized-%03d", i), Operation: core.CapabilitiesGet}
		token := tamperDTLSSignature(t, f.credentials.token(t, request, false))
		frame, err := json.Marshal(adapter.FrameRequest{
			Version:       1,
			CorrelationID: request.RequestID,
			Profile:       "signed",
			Envelope:      token,
		})
		if err != nil {
			t.Fatal(err)
		}
		var datagramID [16]byte
		if _, err := rand.Read(datagramID[:]); err != nil {
			t.Fatal(err)
		}
		unauthorized := webTransportDatagrams(frame, datagramID)
		for _, fragment := range unauthorized {
			if _, err := f.conn.Write(fragment); err != nil {
				t.Fatalf("unauthorized DTLS application record %d: %v", i, err)
			}
		}
		if i < 3 {
			response := readDTLSResponse(t, f.conn, time.Now().Add(2*time.Second))
			if response.Event != "error" || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
				t.Fatalf("tampered DTLS response=%+v", response)
			}
		}
		if !validRenewed && time.Since(started) >= 20*time.Second {
			request := core.Request{Version: 1, RequestID: "dtls-idle-valid-renew", Operation: core.CapabilitiesGet}
			response := dtlsDatagramCall(t, secondFixture, request, true)
			if response.Result == nil || response.Result.Capabilities == nil {
				t.Fatalf("valid renewal response=%+v", response)
			}
			validRenewed = true
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !validRenewed {
		t.Fatal("valid control did not renew the second DTLS association")
	}

	var replacement *dtls.Conn
	for time.Now().Before(started.Add(40 * time.Second)) {
		replacement, err = dialDTLSIdlePeer(f, 2*time.Second)
		if err == nil {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if replacement == nil {
		t.Fatal("DTLS association remained admitted past idle bound")
	}
	defer replacement.Close()
	if elapsed := time.Since(started); elapsed < 28*time.Second || elapsed > 40*time.Second {
		t.Fatalf("DTLS idle slot release elapsed=%s", elapsed)
	}

	// The freed slot must be usable by a fresh authenticated association.
	replacementFixture := f
	replacementFixture.conn = replacement
	if response := dtlsDatagramCall(t, replacementFixture, core.Request{Version: 1, RequestID: "dtls-idle-replacement", Operation: core.CapabilitiesGet}, true); response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("replacement DTLS association unusable: %+v", response)
	}
	secondRequest := core.Request{Version: 1, RequestID: "dtls-idle-valid-after-expiry", Operation: core.CapabilitiesGet}
	if response := dtlsDatagramCall(t, secondFixture, secondRequest, true); response.Result == nil || response.Result.Capabilities == nil {
		t.Fatalf("renewed DTLS association did not remain usable: %+v", response)
	}
}
