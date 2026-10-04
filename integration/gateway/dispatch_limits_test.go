package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func TestDispatchConfiguredFenceDoesNotAdmit(t *testing.T) {
	socket := fixture(t)
	const account = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c, identity := acceptanceJournalFixture(t, socket, account)
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	d, err := dispatch.New(s, v, []core.Operation{core.ReceiptGet})
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.ReadToken(c.Principals[0].BearerTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	r := core.Request{Version: 1, RequestID: "disabled", Operation: core.Send, Target: &core.Target{Kind: "room", Name: "#gateway-fixture"}, Message: "disabled mutation"}
	if _, err := d.Mutation(context.Background(), token, requestJSON(t, r), time.Now()); !errors.Is(err, dispatch.ErrUnsupported) {
		t.Fatal("configured operation fence bypassed", err)
	}
	if _, err := s.Lookup(context.Background(), identity, r.RequestID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unsupported operation entered journal", err)
	}
}
