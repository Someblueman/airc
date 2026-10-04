package dispatch

import (
	"slices"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestWatchCapabilityModesPreserveBothCarriers(t *testing.T) {
	for _, operations := range [][]core.Operation{
		{core.Watch},
		{core.EventsWatch},
		{core.Watch, core.EventsWatch},
	} {
		if got := watchCapabilityModes(operations); !slices.Equal(got, []string{"long_poll", "sse"}) {
			t.Fatalf("operations=%v modes=%v", operations, got)
		}
	}
	if got := watchCapabilityModes([]core.Operation{core.HistoryRead}); got != nil {
		t.Fatalf("unrelated operation advertised watch modes: %v", got)
	}
}

func TestWatchPermissionsDoNotImplicitlyEnableEventsWatch(t *testing.T) {
	implemented := ImplementedOperations()
	got := core.EffectiveOperations(implemented, implemented, []core.Operation{core.Watch}, implemented)
	if !slices.Contains(got, core.Watch) {
		t.Fatalf("watch permission disappeared: %v", got)
	}
	if slices.Contains(got, core.EventsWatch) {
		t.Fatalf("events.watch was implicitly enabled: %v", got)
	}
}
