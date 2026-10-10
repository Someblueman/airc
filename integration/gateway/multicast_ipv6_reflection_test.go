//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestIPv6MulticastRepliesNeverReturnToGroupOrSource(t *testing.T) {
	f := newIPv6MulticastFixture(t)
	iface, err := net.InterfaceByName(f.config.Listeners[0].IPv6Multicast.Interface)
	if err != nil {
		t.Fatal(err)
	}
	group := mustUDPAddress(t, f.address)
	observer, err := net.ListenMulticastUDP("udp6", iface, group)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	request := core.Request{Version: 1, RequestID: "mcast6-no-reflection", Operation: core.CapabilitiesGet}
	frame := adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)}
	body, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	parts := webTransportDatagrams(body, [16]byte{10})
	if err := f.source.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, part := range parts {
		if _, err := f.source.WriteToUDP(part, group); err != nil {
			t.Fatal(err)
		}
	}
	response := readUDPResponse(t, f, time.Now().Add(5*time.Second))
	if response.CorrelationID != frame.CorrelationID || response.Result == nil || response.Result.Capabilities == nil {
		t.Fatal("fixed mailbox did not receive the protected result", response)
	}
	if err := observer.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	for range parts {
		buf := make([]byte, 1001)
		n, source, err := observer.ReadFromUDP(buf)
		if err != nil || source.String() != f.source.LocalAddr().String() {
			t.Fatal("unexpected group source", source, err)
		}
		matched := false
		for _, part := range parts {
			if bytes.Equal(buf[:n], part) && !seen[string(part)] {
				seen[string(part)], matched = true, true
				break
			}
		}
		if !matched {
			t.Fatal("group received a packet other than the original request")
		}
	}
	assertUDPSilent(t, observer)
	assertUDPSilent(t, f.source)
	rpcNoJobs(t, f.config.StateDir)
}
