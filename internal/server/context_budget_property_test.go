package server

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

// A deliberately straightforward oracle repeatedly encodes the full response
// after removing a record. Production admits records in reverse priority and
// maintains byte totals incrementally instead.
func referenceContextWire(entries []protocol.ChatEntry, root string, budget int) (string, bool) {
	entries[len(entries)-1].Context.ProtectedIDs = []string{root}
	for {
		var wire strings.Builder
		for _, e := range entries {
			data, _ := json.Marshal(e)
			wire.WriteString(protocol.Format("server", "777", []string{"reader"}, string(data)))
		}
		wire.WriteString(protocol.Format("server", "778", []string{"reader"}, "End of chat response"))
		if wire.Len() <= budget {
			return wire.String(), true
		}
		summary := entries[len(entries)-1].Context
		remove := -1
		for _, action := range []string{"context-profile", "context-pin", "context-message"} {
			for i, e := range entries {
				if e.Action == action && (e.Message == nil || action != "context-message" || e.Message.ID != root) {
					remove = i
					if action == "context-message" {
						break
					}
				}
			}
			if remove >= 0 {
				switch action {
				case "context-profile":
					summary.OmittedProfiles++
				case "context-pin":
					summary.OmittedPins++
				case "context-message":
					summary.OmittedMessages++
				}
				break
			}
		}
		if remove < 0 {
			return "", false
		}
		entries = append(entries[:remove], entries[remove+1:]...)
	}
}

func FuzzContextWireBudget(f *testing.F) {
	f.Add(uint8(12), "é🐈\n\"<>&", uint16(1600))
	f.Add(uint8(1), strings.Repeat("x", 80), uint16(1024))
	f.Add(uint8(31), "plain", uint16(9999))
	f.Fuzz(func(t *testing.T, count uint8, text string, budgetSeed uint16) {
		if len(text) > 128 {
			t.Skip()
		}
		s, root := contextFixture(1+int(count%32), 0)
		for i := 0; i < s.history.size; i++ {
			// Only change fixture bodies, leaving generated IDs/annotations intact.
			s.history.items[s.history.positions[s.history.at(i).ID]].Body = text
		}
		s.chat.Pins["#room"] = []Message{s.history.at(0)}
		fullEntries, _ := contextResponse(t, s, root, 1000, 0)
		full, _ := referenceContextWire(fullEntries, root, 1<<20)
		for _, budget := range []int{1024 + int(budgetSeed)%8192, max(1024, len(full)-1), max(1024, len(full))} {
			entries, _ := contextResponse(t, s, root, 1000, 0)
			want, fits := referenceContextWire(entries, root, budget)
			gotEntries, got := contextResponse(t, s, root, 1000, budget)
			if fits {
				if got != want {
					t.Fatalf("budget %d: response differs from trimming oracle\ngot %s\nwant %s", budget, got, want)
				}
			} else if len(gotEntries) != 0 || !strings.Contains(got, " 461 ") || strings.Count(got, "\r\n") != 1 {
				t.Fatal("protected-budget failure emitted partial context")
			}
		}
	})
}
