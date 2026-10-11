//go:build linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
	"golang.org/x/sys/unix"
)

func TestUDPLiteUnverifiedInputHasNoResponseOrNativeIntake(t *testing.T) {
	exercisePacketUnverifiedInput(t, newUDPLiteFixture(t))
}

func TestUDPLitePartialChecksumNeverAdmitsProtectedRequest(t *testing.T) {
	f := newUDPLiteFixture(t)
	raw, err := f.source.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	coverage := func(value int) {
		t.Helper()
		var optionErr error
		if err := raw.Control(func(fd uintptr) {
			optionErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_UDPLITE, 10, value)
			if optionErr == nil {
				var actual int
				actual, optionErr = unix.GetsockoptInt(int(fd), unix.IPPROTO_UDPLITE, 10)
				if optionErr == nil && actual != value {
					t.Errorf("send coverage=%d want=%d", actual, value)
				}
			}
		}); err != nil {
			t.Fatal(err)
		}
		if optionErr != nil {
			t.Fatal(optionErr)
		}
	}
	coverage(8)
	request := core.Request{Version: 1, RequestID: "udp-lite-partial", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "partial checksum must never post"}
	writeUDPFrame(t, f, frameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "plain", Request: f.credentials.token(t, request, true)})
	assertUDPSilent(t, f.source)
	assertUDPSilent(t, f.mailbox)
	rpcNoJobs(t, f.config.StateDir)
	if messages := nativeWebTransportMessages(t, f.config.Upstream.Address, "#gateway-fixture"); len(messages) != 0 {
		t.Fatalf("partial checksum posted %+v", messages)
	}
	coverage(0)
	response := udpDatagramCall(t, f, core.Request{Version: 1, RequestID: "udp-lite-after-partial", Operation: core.CapabilitiesGet}, true)
	if response.Result.Capabilities == nil {
		t.Fatal("full coverage recovery missing")
	}
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
