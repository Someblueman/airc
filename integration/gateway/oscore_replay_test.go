//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

func stopOSCOREGateway(t *testing.T, f *oscoreFixture) {
	t.Helper()
	if err := f.process.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-f.process.done:
		if f.process.err != nil {
			t.Fatal(f.process.err, f.process.stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OSCORE gateway did not join")
	}
}

func loadOSCOREState(t *testing.T, f *oscoreFixture) store.OSCOREState {
	t.Helper()
	material, err := auth.ReadOSCOREContext(f.config.Listeners[0].OSCORE.ContextFile)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := material.Digest()
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(t.Context(), f.config)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	state, err := s.LoadOSCORE(t.Context(), store.OSCOREKey{Digest: digest, Principal: material.Owner().Principal, Binding: material.Owner().Binding})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func replayOSCOREPacket(t *testing.T, address string, packet []byte, offset uint16) {
	t.Helper()
	if len(packet) < 4 {
		t.Fatal("no protected POST captured")
	}
	packet = append([]byte(nil), packet...)
	binary.BigEndian.PutUint16(packet[2:4], binary.BigEndian.Uint16(packet[2:4])+offset)
	destination, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialUDP("udp", nil, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write(packet); err != nil {
		t.Fatal(err)
	}
	wire := make([]byte, 2048)
	n, err := conn.Read(wire)
	if err != nil {
		t.Fatal("native replay refusal missing", err)
	}
	response := message.Message{Options: make(message.Options, 0, 32)}
	if _, err := coder.DefaultCoder.Decode(wire[:n], &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != codes.Unauthorized || !bytes.Equal(response.Payload, []byte("Replay detected")) {
		t.Fatal("captured protected packet was not refused as replay", response.Code, string(response.Payload))
	}
}

func TestOSCOREPacketReplayAndStableSendAfterGatewayRestart(t *testing.T) {
	f := newOSCOREFixture(t)
	direct := f.address
	relay := captureOSCORE(t, direct)
	f.address = relay.front.LocalAddr().String()
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "oscore-restart-send", IssuedAt: &issued, ExpiresAt: &expires, Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "OSCORE survives whole gateway restart 🙂"}
	first := oscoreCall(t, f, request)
	if first.Result.Result == nil || !first.Result.Result.Durable {
		t.Fatal("capture did not reach durable intake", first)
	}
	packet := relay.latest()
	relay.close()
	f.address = direct
	accepted := oscoreAccepted(t, f, request)
	replayOSCOREPacket(t, direct, packet, 1)
	stopOSCOREGateway(t, f)
	before := loadOSCOREState(t, f)
	if !before.ReceiverInitialized || before.SenderEnd == 0 {
		t.Fatal("missing durable native replay state", before)
	}
	f.config.Listeners[0].Address = direct
	f.process = startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, f.process)
	f.address = gatewayAddress(t, f.process)
	replayOSCOREPacket(t, f.address, packet, 2)
	stopOSCOREGateway(t, f)
	after := loadOSCOREState(t, f)
	if after.ReceiverInitialized != before.ReceiverInitialized || after.ReceiverSequence != before.ReceiverSequence || after.ReceiverWindow != before.ReceiverWindow || after.SenderEnd < before.SenderEnd {
		t.Fatal("replay changed restored durable receiver state", before, after)
	}
	f.process = startGateway(t, gatewayBinary(t), f.config)
	registerGatewayShutdownCheck(t, f.process)
	f.address = gatewayAddress(t, f.process)
	replay := oscoreCall(t, f, request).Result.Result
	if replay == nil || replay.State != core.Accepted || replay.MessageID != accepted.MessageID || replay.SendAttempts != 1 {
		t.Fatal("stable send replay failed across restart", replay, accepted)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 1 || native[0].ID != accepted.MessageID || native[0].From != "fixture-author" || native[0].Message != request.Message {
		t.Fatal("restart replay duplicated or changed AIRC message", native)
	}
}
