//go:build darwin || linux

package gateway_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func assertUDPSilent(t *testing.T, socket *net.UDPConn) {
	t.Helper()
	if err := socket.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	n, _, err := socket.ReadFromUDP(make([]byte, 1001))
	var timeout net.Error
	if n != 0 || !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("unexpected UDP output bytes=%d err=%v", n, err)
	}
}

func TestUDPUnverifiedInputHasNoResponseOrNativeIntake(t *testing.T) {
	f := newUDPFixture(t)
	address, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"signed", "tampered", "realm", "denied", "override", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			request := core.Request{Version: 1, RequestID: "udp-invalid-" + kind, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must never post"}
			if kind == "denied" {
				request.Target.Name = "#denied"
			}
			credentials := *f.credentials
			if kind == "realm" {
				credentials.realm = "other"
			}
			frame := adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: credentials.token(t, request, true)}
			if kind == "signed" {
				frame.Profile = "signed"
				frame.Envelope = credentials.token(t, request, false)
			}
			if kind == "tampered" {
				parts := strings.Split(frame.Envelope, ".")
				tag, err := base64.RawURLEncoding.DecodeString(parts[4])
				if err != nil || len(tag) == 0 {
					t.Fatal("invalid encrypted test token", err)
				}
				tag[0] ^= 1
				parts[4] = base64.RawURLEncoding.EncodeToString(tag)
				frame.Envelope = strings.Join(parts, ".")
			}
			if kind == "oversize" {
				if _, err := f.source.WriteToUDP(make([]byte, 1001), address); err != nil {
					t.Fatal(err)
				}
			} else if kind == "override" {
				body, err := json.Marshal(frame)
				if err != nil {
					t.Fatal(err)
				}
				body = append(body[:len(body)-1], []byte(`,"destination":"127.0.0.1:9"}`)...)
				for _, fragment := range webTransportDatagrams(body, [16]byte{5}) {
					if _, err := f.source.WriteToUDP(fragment, address); err != nil {
						t.Fatal(err)
					}
				}
			} else {
				writeUDPFrame(t, f, frame)
			}
			assertUDPSilent(t, f.source)
			assertUDPSilent(t, f.mailbox)
			rpcNoJobs(t, f.config.StateDir)
		})
	}
	if messages := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("invalid UDP input posted %+v", messages)
	}
	// The same source can authenticate afterward, but only the fixed mailbox
	// receives its encrypted response; the source receives no reflected packet.
	udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-after-invalid", Operation: core.CapabilitiesGet}, true)
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
