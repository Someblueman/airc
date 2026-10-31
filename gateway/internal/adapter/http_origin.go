package adapter

import (
	"net/http"
	"strings"

	"github.com/Someblueman/airc/gateway/internal/core"
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
			if seen[field] || field != "content-type" && field != "idempotency-key" && field != "last-event-id" && field != "accept" && field != "airc-nick" && field != "airc-token" && field != "airc-password" {
				httpFailure(w, http.StatusForbidden, core.Forbidden)
				return false
			}
			seen[field] = true
		}
	}
	w.Header().Set("Access-Control-Allow-Methods", methods[0])
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Last-Event-ID, Accept, AIRC-Nick, AIRC-Token, AIRC-Password")
	w.Header().Set("Access-Control-Max-Age", "300")
	// No Allow-Credentials: browser clients must use Fetch credentials:"omit".
	w.WriteHeader(http.StatusNoContent)
	return false
}

func corsRoute(path, method string) bool {
	if method == http.MethodPost {
		return path == "/v1/messages" || path == "/v1/request" || path == "/v1/rpc" || path == "/v1/rpc/events"
	}
	if method == http.MethodPut {
		return requestResourceID(path) != ""
	}
	if method != http.MethodGet {
		return false
	}
	if path == "/v1/capabilities" || readPaths[path] {
		return true
	}
	return strings.HasPrefix(path, "/v1/requests/") && len(path) > len("/v1/requests/")
}
