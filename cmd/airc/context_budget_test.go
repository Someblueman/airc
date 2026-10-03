package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/pkg/irc"
)

// Differential oracle: the previous repeated-serialization policy. This checks
// exact records, ordering, omission counts and bytes, not just the output size.
func oldContextBudget(r *conversationContext, protected map[string]bool, budget int) ([]byte, bool) {
	for {
		data, _ := json.Marshal(r)
		if len(data)+1 <= budget {
			return data, true
		}
		switch {
		case len(r.Participants) > 0:
			r.Participants = r.Participants[:len(r.Participants)-1]
			r.OmittedProfiles++
		case len(r.Pins) > 0:
			r.Pins = r.Pins[:len(r.Pins)-1]
			r.OmittedPins++
		default:
			i := 0
			for i < len(r.Messages) && protected[r.Messages[i].ID] {
				i++
			}
			if i == len(r.Messages) {
				return nil, false
			}
			r.Messages = append(r.Messages[:i], r.Messages[i+1:]...)
			r.OmittedMessages++
		}
	}
}

func TestContextBudgetMatchesOriginalPolicy(t *testing.T) {
	r := conversationContext{ContextSummary: protocol.ContextSummary{RootID: "root", TriggerID: "trigger", OmittedMessages: 9, OmittedPins: 99, OmittedProfiles: 9}}
	for i := range 20 {
		r.Messages = append(r.Messages, checkMessage{ID: fmt.Sprint(i), Message: strings.Repeat("\"<>&\\\n🙂\u2028", i+1)})
	}
	r.Messages[0].ID, r.Messages[10].ID, r.Messages[19].ID = "root", "trigger", "correction"
	r.Messages[10].SupersededBy = "correction"
	r.Pins = append([]checkMessage{}, r.Messages[:3]...)
	r.Participants = []irc.AgentCard{{Nick: "<planner>"}, {Nick: "🙂"}}
	encoded, _ := json.Marshal(r)
	protected := map[string]bool{"root": true, "trigger": true, "correction": true}
	for budget := 1; budget <= len(encoded)+2; budget += 37 {
		var old, next conversationContext
		_ = json.Unmarshal(encoded, &old)
		_ = json.Unmarshal(encoded, &next)
		want, ok := oldContextBudget(&old, protected, budget)
		got, err := budgetContext(&next, protected, budget)
		if (err == nil) != ok || ok && string(got) != string(want) {
			t.Fatalf("budget %d: error=%v\ngot %s\nwant %s", budget, err, got, want)
		}
		if ok {
			// Boundary counts the newline, including an exact fit.
			for _, exact := range []int{len(got), len(got) + 1} {
				_ = json.Unmarshal(encoded, &old)
				_ = json.Unmarshal(encoded, &next)
				want, ok = oldContextBudget(&old, protected, exact)
				got, err = budgetContext(&next, protected, exact)
				if (err == nil) != ok || ok && string(got) != string(want) {
					t.Fatalf("exact boundary %d differs: %v", exact, err)
				}
			}
		}
	}
}
