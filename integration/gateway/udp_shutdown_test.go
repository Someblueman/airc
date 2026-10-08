//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestUDPActiveWatchAndIncompleteRequestJoinOnSIGINT(t *testing.T) {
	f := newUDPFixture(t)
	exercisePacketWatchShutdown(t, f)
}

func exercisePacketWatchShutdown(t *testing.T, f udpFixture) {
	t.Helper()
	socket := f.config.Bindings[0].Address
	statusClient := nativeStatusClient(t, socket)
	baseline := nativeObserverCount(t, statusClient)
	watch := core.Request{Version: 1, RequestID: "udp-shutdown-watch", Operation: core.Watch, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Start: "retained", Limit: 8}
	writeUDPFrame(t, f, adapter.FrameRequest{Version: 1, CorrelationID: watch.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, watch, true), Stream: true})
	if page := readUDPResponse(t, f, time.Now().Add(3*time.Second)); page.Event != "page" || page.Page == nil {
		t.Fatalf("UDP initial watch=%+v", page)
	}
	waitNativeObserverCount(t, statusClient, baseline+1)
	request := core.Request{Version: 1, RequestID: "udp-incomplete", Operation: core.Send, Target: watch.Target, Message: strings.Repeat("x", 3500)}
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	fragments := webTransportDatagrams(body, [16]byte{9})
	if len(fragments) < 2 {
		t.Fatal("incomplete control did not fragment")
	}
	address, err := net.ResolveUDPAddr("udp", f.address)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.source.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.source.WriteToUDP(fragments[0], address); err != nil {
		t.Fatal(err)
	}
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("UDP SIGINT: %v %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("UDP SIGINT did not join workers")
	}
	bound, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: address.Port})
	if err != nil {
		t.Fatalf("UDP listener retained after SIGINT: %v", err)
	}
	bound.Close()
	waitNativeLeaseGone(t, statusClient, baseline)
	rpcNoJobs(t, f.config.StateDir)
}
