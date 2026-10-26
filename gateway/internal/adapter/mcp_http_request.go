package adapter

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Someblueman/airc/gateway/internal/store"
)

func mcpBody(r *http.Request) ([]byte, bool, error) {
	if r.Body == nil || r.ContentLength > mcpHTTPBodyLimit {
		return nil, false, store.ErrCapacity
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, mcpHTTPBodyLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > mcpHTTPBodyLimit {
		return nil, false, store.ErrCapacity
	}
	var batch []json.RawMessage
	if len(data) == 0 {
		return nil, false, store.ErrInvalid
	}
	if data[0] == '[' {
		return nil, false, store.ErrInvalid
	} else {
		batch = []json.RawMessage{data}
	}
	initialize := false
	for _, raw := range batch {
		var msg struct {
			Method string `json:"method"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return nil, false, err
		}
		if msg.Method == "initialize" {
			initialize = true
		}
		if err := validateMCPJSONBounds(raw); err != nil {
			return nil, false, err
		}
	}
	return data, initialize, nil
}

func mcpOrigin(w http.ResponseWriter, r *http.Request, allowed map[string]bool) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 || !allowed[values[0]] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", values[0])
	w.Header().Add("Vary", "Origin")
	return true
}

const mcpFieldLimit = 256

func validateMCPJSONBounds(raw []byte) error {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return validateMCPJSONValue(value, "")
}

func validateMCPJSONValue(value any, key string) error {
	switch v := value.(type) {
	case string:
		if key == "id" || key == "progressToken" || key == "method" || key == "name" {
			encoded, err := json.Marshal(v)
			if err != nil || len(encoded) > mcpFieldLimit {
				return store.ErrCapacity
			}
		}
	case float64:
		if key == "id" {
			encoded, _ := json.Marshal(v)
			if len(encoded) > mcpFieldLimit {
				return store.ErrCapacity
			}
		}
	case map[string]any:
		for k, child := range v {
			if err := validateMCPJSONValue(child, k); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := validateMCPJSONValue(child, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func mcpPreflight(w http.ResponseWriter, r *http.Request, allowed map[string]bool) {
	if len(r.Header.Values("Origin")) != 1 || !allowed[r.Header.Get("Origin")] {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	methods := r.Header.Values("Access-Control-Request-Method")
	if len(methods) != 1 || (methods[0] != http.MethodPost && methods[0] != http.MethodGet && methods[0] != http.MethodDelete) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	requested := r.Header.Values("Access-Control-Request-Headers")
	if len(requested) > 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	seen := map[string]bool{}
	if len(requested) == 1 {
		for _, field := range strings.Split(requested[0], ",") {
			field = strings.ToLower(strings.TrimSpace(field))
			if field == "" || seen[field] || field != "content-type" && field != "accept" && field != "mcp-session-id" && field != "mcp-protocol-version" && field != "last-event-id" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			seen[field] = true
		}
	}
	w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
	w.Header().Add("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
	w.Header().Set("Access-Control-Expose-Headers", "Mcp-Session-Id, Mcp-Protocol-Version, Last-Event-ID")
	w.WriteHeader(http.StatusNoContent)
}
