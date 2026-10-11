package adapter

import (
	"errors"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func postgresFixtureRoute() config.PostgresNotifyRoute {
	return config.PostgresNotifyRoute{Target: core.Target{Kind: "room", Name: "#fixture"}}
}

// TestPostgresNotifyClosedJournalEndsOwner: only a closed journal ends the
// owner; TestCarrierAdmission covers intake, replay and refusals.
func TestPostgresNotifyClosedJournalEndsOwner(t *testing.T) {
	f := newRouteFixture(t)
	d := f.route(t, "pg", core.Send)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := admitPostgresNotify(t.Context(), d, f.author(t, "", false, true), "route:pg", postgresFixtureRoute(), []byte(plainRouteSend), make(chan struct{}, 1)); !errors.Is(err, store.ErrClosed) {
		t.Fatal("closed journal must end the owner", err)
	}
}
