package gateway_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestHTTPDetachedCapabilities(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := httpGatewayFixture(t, version == 2, nil, false, true)
			r := core.Request{Version: 1, RequestID: "discover", Operation: core.CapabilitiesGet}
			for _, encrypted := range []bool{false, true} {
				status, proto, result := detachedCall(t, f, f.credentials.token(t, r, encrypted), encrypted)
				c := result.Capabilities
				if status != 200 || proto != version || result.Operation != core.CapabilitiesGet || c == nil || result.Result != nil || result.Page != nil || result.Directory != nil {
					t.Fatal("invalid detached discovery result", status, proto, result)
				}
				if !slices.Contains(c.Operations, core.Send) || slices.Contains(c.Operations, core.CapabilitiesGet) || !reflect.DeepEqual(c.WatchModes, []string{"long_poll", "sse"}) || c.UpstreamCompatible != nil {
					t.Fatal("discovery advertised unsupported capabilities", c)
				}
				if !reflect.DeepEqual(c.Targets, f.identity.Targets()) {
					t.Fatal("discovery changed caller scope", c.Targets)
				}
			}
			if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
				t.Fatal(err)
			}
			for _, encrypted := range []bool{false, true} {
				status, _, result := detachedCall(t, f, f.credentials.token(t, r, encrypted), encrypted)
				if status != 403 || result.Capabilities != nil {
					t.Fatal("discovery escaped durable revocation", status, result)
				}
			}
		})
	}
}
