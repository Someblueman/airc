//go:build !linux || (!arm64 && !amd64)

package service

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestPOSIXIngressUnsupportedBeforeJournalAccess(t *testing.T) {
	l := config.Listener{ID: "queue", Protocol: "posix_mqueue", Address: "/queue", POSIXQueue: &config.POSIXQueue{Depth: 1}}
	// An unopened store proves unsupported platforms fail before journal access.
	q, err := openPOSIXIngress(t.Context(), &store.Store{}, l)
	if q != nil || !errors.Is(err, adapter.ErrPOSIXQueueUnsupported) {
		t.Fatalf("%v %v", q, err)
	}
}
