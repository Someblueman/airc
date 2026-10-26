//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Someblueman/airc/pkg/irc"
)

func channelHistorySnapshot(t *testing.T, socket string) map[string]string {
	t.Helper()
	probe := dial(t, socket, "channels-history-probe", "", false, true)
	defer probe.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	entries, err := irc.RequestCheck(ctx, probe, irc.CheckRequest{Targets: []irc.CheckTarget{{Target: "#channels-history", Limit: 64}}, IncludeOwn: true, MaxMessages: 128}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	for _, entry := range entries {
		if entry.Message != nil {
			body, err := json.Marshal(entry.Message)
			if err != nil {
				t.Fatal(err)
			}
			result[entry.Message.ID] = string(body)
		}
	}
	if len(result) != 2 {
		t.Fatalf("seeded history snapshot=%+v", result)
	}
	return result
}
