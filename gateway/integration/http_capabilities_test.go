package gateway_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

// Capabilities describe the listener, the same for every author: a guest and
// a registered author see one document, without internal authority.
func TestHTTPCapabilitiesDescribeTheListener(t *testing.T) {
	t.Parallel()
	f := gatewayHTTPFixture(t, true)
	want := []core.Operation{core.Send, core.Reply, core.ReceiptGet, core.HistoryRead, core.Search, core.Watch, core.Directory, core.AgentsRead}
	for _, token := range []string{f.token, f.other, ""} {
		status, protocol, data := httpCall(t, f, http.MethodGet, "/v1/capabilities", token, nil)
		var capabilities dispatch.Capabilities
		if err := json.Unmarshal(data, &capabilities); err != nil || status != 200 || protocol != 2 {
			t.Fatal("HTTP capability response failed", status, string(data), err)
		}
		if capabilities.Version != 1 || !slices.Equal(capabilities.Operations, want) || len(capabilities.Targets) != 0 || capabilities.MaxRequestBytes != core.MaxRequestBytes || capabilities.UpstreamCompatible != nil {
			t.Fatal("capabilities overstated permission or upstream knowledge", capabilities)
		}
		if capabilities.MaxHistoryBytes != backend.MaxHistoryBytes || capabilities.MaxSearchBytes != backend.MaxHistoryBytes || capabilities.MaxWatchBytes != backend.MaxHistoryBytes || capabilities.WatchMode != "long_poll" {
			t.Fatal("read capabilities missing", capabilities)
		}
		if !slices.Equal(capabilities.DirectoryModes, []string{"snapshot", "nickname"}) || capabilities.DirectoryScope != "public_global" || capabilities.MaxDirectoryBytes != backend.MaxDirectoryBytes || capabilities.MaxDirectoryCards != 256 {
			t.Fatal("directory capabilities missing", capabilities)
		}
		if capabilities.AgentsScope != "public_global" || capabilities.MaxAgents != core.MaxAgents || capabilities.MaxAgentsBytes != core.MaxAgentsBytes {
			t.Fatal("live agent capabilities missing", capabilities)
		}
		if strings.Contains(string(data), "fixture-author") || strings.Contains(string(data), "token_file") || strings.Contains(string(data), "password_file") {
			t.Fatal("capabilities exposed internal authority", string(data))
		}
	}
}
