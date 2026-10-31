//go:build darwin || linux

package gateway_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestPinnedFixtureSharesOnlyImmutableBinary(t *testing.T) {
	t.Parallel()
	first, second := fixture(t), fixture(t)
	provision := dial(t, first, "fixture-author", fixtureAccount, true, true)
	_ = provision.Close()
	parent := seedPinMutationMessage(t, first, "#gateway-fixture", "fixture-isolation")
	if retained := topicHistorySnapshot(t, first); retained[parent] == "" {
		t.Fatal("first fixture positive control has no retained message")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	unexpected, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: second, Nick: "fixture-author", IdentityToken: fixtureAccount, Ephemeral: true, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if unexpected != nil {
		_ = unexpected.Close()
	}
	var rejected *irc.RejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("second fixture accepted first fixture's account: %v", err)
	}
	reader := dial(t, second, "isolation-reader", "", false, true)
	defer reader.Close()
	entries, err := irc.RequestCheck(ctx, reader, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#gateway-fixture", Limit: 32}}, IncludeOwn: true, MaxMessages: 32}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Message != nil {
			t.Fatalf("second fixture inherited native history: %+v", entry.Message)
		}
	}
}
