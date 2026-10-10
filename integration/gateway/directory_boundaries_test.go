package gateway_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/pkg/irc"
)

func TestDirectoryNativeFeatureMalformedDuplicateAndJoinedTimeout(t *testing.T) {
	for _, test := range []struct {
		mode   string
		status int
	}{
		{"directory-feature-zero", 501}, {"directory-malformed", 503}, {"directory-duplicate", 503}, {"withhold-directory", 503},
	} {
		t.Run(test.mode, func(t *testing.T) {
			f := gatewayHTTPFixture(t, true, test.mode)
			began := time.Now()
			status, _, data := httpCall(t, f, http.MethodGet, "/v1/directory", f.token, nil)
			if status != test.status || strings.Contains(string(data), `"agents"`) || time.Since(began) > 5*time.Second {
				t.Fatal("directory boundary failed", status, string(data), time.Since(began))
			}
			select {
			case <-f.watchJoined:
			case <-time.After(time.Second):
				t.Fatal("failed directory retained upstream work")
			}
		})
	}
}

func TestDirectoryNativeByteOverflowReturnsNoPartialCards(t *testing.T) {
	f := gatewayHTTPFixture(t, true)
	for i := 0; i < 40; i++ {
		nick := "large-profile-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		c := dial(t, f.socket, nick, "", false, true)
		field := strings.Repeat("x", 400)
		if err := c.UpdateProfile(map[string]string{"model": field, "workspace": field, "tools": field, "about": field}); err != nil {
			t.Fatal(err)
		}
		next(t, c, func(e *irc.EndOfDirectoryEvent) bool { return true })
		_ = c.Close()
	}
	status, _, data := httpCall(t, f, http.MethodGet, "/v1/directory", f.token, nil)
	if status != 429 || strings.Contains(string(data), `"agents"`) {
		t.Fatal("overflow returned partial directory", status, string(data))
	}
	status, _, data = httpCall(t, f, http.MethodGet, "/v1/directory?nick=large-profile-aa&limit=1", f.token, nil)
	var snapshot backend.DirectorySnapshot
	if status != 200 || json.Unmarshal(data, &snapshot) != nil || len(snapshot.Agents) != 1 || snapshot.Agents[0].Nick != "large-profile-aa" || len(snapshot.Agents[0].About) != 400 {
		t.Fatal("nickname lookup scanned overflowing global directory", status, string(data))
	}
}
