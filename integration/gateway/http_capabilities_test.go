package gateway_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func TestHTTPCapabilitiesScopedAuthenticatedAndRevocable(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	for _, tc := range []struct {
		token      string
		operations []core.Operation
		target     string
	}{
		{f.token, []core.Operation{core.Send, core.Reply, core.ReceiptGet, core.HistoryRead}, "#gateway-fixture"},
		{f.other, []core.Operation{core.ReceiptGet}, "#other-private"},
	} {
		status, protocol, data := httpCall(t, f, http.MethodGet, "/v1/capabilities", tc.token, nil)
		var capabilities dispatch.Capabilities
		if err := json.Unmarshal(data, &capabilities); err != nil || status != 200 || protocol != 2 {
			t.Fatal("HTTP capability response failed", status, string(data), err)
		}
		if capabilities.Version != 1 || !slices.Equal(capabilities.Operations, tc.operations) || len(capabilities.Targets) != 1 || capabilities.Targets[0].Name != tc.target || capabilities.MaxEnvelopeBytes != core.MaxEnvelopeBytes || capabilities.UpstreamCompatible != nil {
			t.Fatal("capabilities overstated permission or upstream knowledge", capabilities)
		}
		if tc.token == f.token && (len(capabilities.HistoryTargets) != 1 || capabilities.HistoryTargets[0].Name != tc.target || capabilities.MaxHistoryBytes != backend.MaxHistoryBytes) || tc.token == f.other && (len(capabilities.HistoryTargets) != 0 || capabilities.MaxHistoryBytes != 0) {
			t.Fatal("history capabilities not scoped", capabilities)
		}
		if strings.Contains(string(data), "fixture-author") || strings.Contains(string(data), "token_file") || strings.Contains(string(data), "binding") {
			t.Fatal("capabilities exposed internal authority", string(data))
		}
	}
	status, _, _ := httpCall(t, f, http.MethodGet, "/v1/capabilities", strings.Repeat("f", 64), nil)
	if status != 401 {
		t.Fatal("unauthenticated capability discovery allowed", status)
	}
	if _, err := f.journal.Revoke(t.Context(), f.identity.ID()); err != nil {
		t.Fatal(err)
	}
	status, _, _ = httpCall(t, f, http.MethodGet, "/v1/capabilities", f.token, nil)
	if status != 403 {
		t.Fatal("capability discovery bypassed durable revocation", status)
	}
	status, _, _ = httpCall(t, f, http.MethodGet, "/v1/capabilities", f.other, nil)
	if status != 200 {
		t.Fatal("one principal revocation affected another", status)
	}
}
