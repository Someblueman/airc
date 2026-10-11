package gateway_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/backend"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestDispatchListenerOperationsDoNotAdmit(t *testing.T) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, identity := acceptanceJournalFixture(t, socket, account)
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	b, err := backend.New(c)
	if err != nil {
		t.Fatal(err)
	}
	d, err := dispatch.ForListener(s, b, nil, config.Listener{ID: "receipts", Protocol: "http", Operations: []core.Operation{core.ReceiptGet}})
	if err != nil {
		t.Fatal(err)
	}
	discovery := core.Request{Version: 1, RequestID: "caps", Operation: core.CapabilitiesGet}
	result, err := d.Submit(context.Background(), dispatch.Ingress{}, []byte(authorRequest(t, discovery, fixtureAuthor, account)), time.Now())
	if err != nil || result.Capabilities == nil || !slices.Contains(result.Capabilities.Operations, core.ReceiptGet) || slices.Contains(result.Capabilities.Operations, core.Send) {
		t.Fatal("capabilities bypassed the listener's operations", result.Capabilities, err)
	}
	r := core.Request{Version: 1, RequestID: "disabled", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "disabled mutation"}
	if _, err := d.Submit(context.Background(), dispatch.Ingress{}, []byte(authorRequest(t, r, fixtureAuthor, account)), time.Now()); err == nil {
		t.Fatal("listener operations bypassed")
	}
	if _, err := s.LookupAuthor(context.Background(), identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("refused operation entered journal", err)
	}
}
