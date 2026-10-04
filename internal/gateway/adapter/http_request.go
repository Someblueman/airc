package adapter

import (
	"strings"

	"github.com/Someblueman/airc/internal/gateway/core"
)

// requestResourceID accepts exactly one stable request ID beneath the status URI.
func requestResourceID(path string) string {
	const prefix = "/v1/requests/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	id := strings.TrimPrefix(path, prefix)
	r := core.Request{Version: 1, RequestID: id, Operation: core.ReceiptGet, LookupID: id}
	if r.Normalize() != nil {
		return ""
	}
	return id
}
