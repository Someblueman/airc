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

func TestDTLSActiveWatchAndIncompleteDatagramJoinOnSIGINT(t *testing.T) {
	f := newDTLSFixture(t)
	socket := f.config.Bindings[0].Address
	baseline := nativeObserverCount(t, socket)
	writeDTLSWatch(t, f, "dtls-shutdown-watch")
	page := readDTLSResponse(t, f.conn, time.Now().Add(3*time.Second))
	if page.Event != "page" || page.Page == nil {
		t.Fatalf("DTLS shutdown initial=%+v", page)
	}
	waitNativeObserverCount(t, socket, baseline+1)
	request := core.Request{Version: 1, RequestID: "dtls-shutdown-incomplete", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: strings.Repeat("p", 3500)}
	body, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: "dtls-incomplete", Profile: "encrypted", Envelope: f.credentials.token(t, request, true)})
	if err != nil {
		t.Fatal(err)
	}
	grams := webTransportDatagrams(body, [16]byte{1, 2, 3})
	if len(grams) < 2 {
		t.Fatal("shutdown control did not fragment")
	}
	if _, err := f.conn.Write(grams[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatalf("DTLS SIGINT failed: %v %s", f.process.err, f.process.stderr.String())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DTLS SIGINT did not join")
	}
	packet, err := net.ListenPacket("udp", f.address)
	if err != nil {
		t.Fatalf("DTLS retained UDP socket: %v", err)
	}
	_ = packet.Close()
	waitNativeLeaseGone(t, socket, baseline)
	rpcNoJobs(t, f.config.StateDir)
}
