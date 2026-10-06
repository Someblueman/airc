//go:build darwin || linux

package gateway_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestDirectoryMailboxRefusesOtherAuthenticatedOwner(t *testing.T) {
	f := prepareDirectoryMailboxFixture(t)
	other := mailboxOtherSigner(t, f.fileMailboxFixture)
	f.start(t)
	verifier, err := auth.New(f.config)
	if err != nil {
		t.Fatal(err)
	}
	for _, encrypted := range []bool{false, true} {
		request := core.Request{Version: 1, RequestID: "directory-other-owner", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "must not bypass directory owner"}
		profile := "signed"
		if encrypted {
			profile = "encrypted"
			request.RequestID += "-encrypted"
		}
		token := other.token(t, request, encrypted)
		// Prove the configured other identity really authenticates, rather than
		// treating a malformed or unknown signing key as owner isolation.
		var identity auth.Identity
		if encrypted {
			message, err := verifier.AuthenticateEncrypted(token, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			identity = message.Identity
		} else {
			message, err := verifier.AuthenticateSigned(token, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			identity = message.Identity
		}
		if identity.ID() != "other-device" || identity.Binding() != f.config.Principals[0].Binding {
			t.Fatal("wrong independently verified identity")
		}
		data, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: request.RequestID, Profile: profile, Envelope: token})
		if err != nil {
			t.Fatal(err)
		}
		response := directoryMailboxDocument(t, f, request.RequestID, data)
		if response.CorrelationID != request.RequestID || response.Error == nil || response.Error.Code != core.AuthenticationFailed {
			t.Fatal("other directory owner admitted", response)
		}
		noMailboxJob(t, f.fileMailboxFixture, request.RequestID)
	}
	if native := nativeWebTransportMessages(t, f.config.Bindings[0].Address, "#gateway-fixture"); len(native) != 0 {
		t.Fatal("owner refusals changed native history", native)
	}
}
