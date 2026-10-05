//go:build darwin || linux

package gateway_test

import (
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/plgd-dev/go-coap/v3/message"
)

func TestCoAPSAllSharedOperationsSignedAndEncrypted(t *testing.T) {
	for _, mode := range []struct {
		label     string
		encrypted bool
		typ       message.Type
	}{
		{label: "coaps-signed", typ: message.Confirmable},
		{label: "coaps-encrypted", encrypted: true, typ: message.Confirmable},
		{label: "coaps-non-signed", typ: message.NonConfirmable},
		{label: "coaps-non-encrypted", encrypted: true, typ: message.NonConfirmable},
	} {
		t.Run(mode.label, func(t *testing.T) {
			f := newCoAPFixture(t)
			call := func(request core.Request, _ bool) adapter.FrameResponse {
				return coapRPCType(t, f, request, mode.encrypted, mode.typ)
			}
			accepted := func(request core.Request, _ bool) core.Result {
				return coapAcceptedType(t, f, request, mode.encrypted, mode.typ)
			}
			exerciseNativeOperations(t, call, accepted, nativeOperationFixture{config: f.config, observer: f.observer}, mode.label)
		})
	}
}
