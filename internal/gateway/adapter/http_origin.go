package adapter

import (
	"net/http"
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// CORS grants browser visibility, never authority. Actual requests always pass
// normal authentication. Preflights do not allocate upstream sessions.
func httpOrigin(w http.ResponseWriter, r *http.Request, allowed map[string]bool) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		if r.Method == http.MethodOptions {
			httpFailure(w, http.StatusForbidden, core.Forbidden)
			return false
		}
		return true
	}
	if len(values) != 1 || !allowed[values[0]] {
		httpFailure(w, http.StatusForbidden, core.Forbidden)
		return false
	}
	if r.Method != http.MethodOptions {
		return true
	}
	w.Header().Add("Vary", "Access-Control-Request-Method")
	w.Header().Add("Vary", "Access-Control-Request-Headers")
	methods := r.Header.Values("Access-Control-Request-Method")
	headers := r.Header.Values("Access-Control-Request-Headers")
	if len(methods) != 1 || len(headers) > 1 || !corsRoute(r.URL.Path, methods[0]) {
		httpFailure(w, http.StatusForbidden, core.Forbidden)
		return false
	}
	seen := map[string]bool{}
	if len(headers) == 1 {
		for _, field := range strings.Split(headers[0], ",") {
			field = strings.ToLower(strings.TrimSpace(field))
			if seen[field] || field != "authorization" && field != "content-type" && field != "idempotency-key" && field != "last-event-id" && field != "accept" {
				httpFailure(w, http.StatusForbidden, core.Forbidden)
				return false
			}
			seen[field] = true
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", methods[0])
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key, Last-Event-ID, Accept")
	w.Header().Set("Access-Control-Max-Age", "300")
	// No Allow-Credentials: browser clients must use Fetch credentials:"omit".
	w.WriteHeader(http.StatusNoContent)
	return false
}

func corsRoute(path, method string) bool {
	if method == http.MethodPost {
		return path == "/v1/messages" || path == "/v1/rpc" || path == "/v1/rpc/events" || path == "/v1/envelopes/signed" || path == "/v1/envelopes/encrypted" || path == "/v1/envelopes/signed/events" || path == "/v1/envelopes/encrypted/events"
	}
	if method == http.MethodPut {
		return requestResourceID(path) != ""
	}
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/v1/capabilities", "/v1/history", "/v1/search", "/v1/watch", "/v1/events", "/v1/live", "/v1/live/events", "/v1/directory", "/v1/channels", "/v1/agents", "/v1/context", "/v1/pins", "/v1/room", "/v1/waiting", "/v1/poll", "/v1/topic", "/v1/members":
		return true
	}
	return strings.HasPrefix(path, "/v1/requests/") && len(path) > len("/v1/requests/")
}
