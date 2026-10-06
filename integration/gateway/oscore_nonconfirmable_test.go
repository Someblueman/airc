//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/codes"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

func oscoreNonconfirmableSend(t *testing.T, f *oscoreFixture, request core.Request) adapter.FrameResponse {
	t.Helper()
	relay := captureOSCORE(t, f.address)
	defer relay.close()
	command, sequence, output := oscoreNativeCommand(t, f, oscoreInput(t, f, request, 0), relay.front.LocalAddr().String(), oscoreSequence(t, f.sequence), 1024)
	command.Args = append(command.Args[:len(command.Args)-1], "-N", command.Args[len(command.Args)-1])
	if diagnostic, err := command.CombinedOutput(); err != nil {
		t.Fatalf("native non-confirmable send failed: %v %s", err, diagnostic)
	}
	resumeOSCORESequence(t, f, sequence)
	packet := message.Message{Options: make(message.Options, 0, 32)}
	if _, err := coder.DefaultCoder.Decode(relay.latest(), &packet); err != nil || packet.Type != message.NonConfirmable || packet.Code != codes.POST || !packet.Options.HasOption(message.OptionID(9)) {
		t.Fatal("native client did not send a protected non-confirmable POST", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(data, &response); err != nil || response.Version != 1 || response.CorrelationID != request.RequestID || response.Error != nil || response.Result == nil || response.Result.Result == nil || !response.Result.Result.Durable || response.Result.Result.RequestID != request.RequestID {
		t.Fatal("native non-confirmable send did not receive durable intake", err, response)
	}
	return response
}

func TestOSCORENonconfirmableSendAcceptanceAndStableReplay(t *testing.T) {
	f := newOSCOREFixture(t)
	issued, expires := time.Now().Unix(), time.Now().Unix()+120
	request := core.Request{Version: 1, RequestID: "oscore-nonconfirmable-send", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "native protected NON send 🙂", IssuedAt: &issued, ExpiresAt: &expires}
	_ = oscoreNonconfirmableSend(t, f, request)
	accepted := oscoreAccepted(t, f, request)
	event := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == accepted.MessageID })
	if event.From != "fixture-author" || event.Target != request.Target.Name || event.Message != request.Message {
		t.Fatal("non-confirmable independent observer mismatch", event)
	}
	_ = oscoreNonconfirmableSend(t, f, request)
	replayed := oscoreAccepted(t, f, request)
	if replayed.MessageID != accepted.MessageID || replayed.SendAttempts != 1 {
		t.Fatal("non-confirmable signed-intent replay changed acceptance", replayed, accepted)
	}
	native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, request.Target.Name)
	if len(native) != 1 || native[0].ID != accepted.MessageID || native[0].From != "fixture-author" || native[0].Message != request.Message {
		t.Fatal("non-confirmable send did not yield one exact AIRC record", native)
	}
}
