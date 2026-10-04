package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

func FuzzContextBudget(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4}, "é🐈\n\"<>&\\\u2028", uint16(1024))
	f.Add([]byte{}, "", uint16(1))
	f.Add([]byte{255, 99, 9}, "code\n    return value", uint16(65535))
	f.Fuzz(func(t *testing.T, shape []byte, text string, seed uint16) {
		if len(shape) > 24 || len(text) > 128 || !utf8.ValidString(text) {
			t.Skip()
		}
		r := conversationContext{RootID: "root", TriggerID: "trigger", OmittedMessages: 9, OmittedPins: 99, OmittedProfiles: 999}
		protected := map[string]bool{}
		for i, b := range shape {
			id := fmt.Sprint(i)
			r.Messages = append(r.Messages, checkMessage{ID: id, Message: text + fmt.Sprint(b), ReplyTo: "parent"})
			if b&1 != 0 {
				protected[id] = true
			}
			if b&2 != 0 {
				r.Pins = append(r.Pins, r.Messages[i])
			}
			if b&4 != 0 {
				r.Participants = append(r.Participants, irc.AgentCard{Nick: id})
			}
		}
		if len(r.Messages) > 0 {
			r.RootID = r.Messages[0].ID
			protected[r.RootID] = true
			r.TriggerID = r.Messages[len(r.Messages)-1].ID
			protected[r.TriggerID] = true
		}
		original, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		// Include small budgets, omission-counter digit transitions and exact wire
		// boundaries. The oracle repeatedly serializes the old, straightforward policy.
		budgets := []int{1 + int(seed)%(len(original)+2), len(original), len(original) + 1}
		for _, budget := range budgets {
			var next, reference conversationContext
			if json.Unmarshal(original, &next) != nil || json.Unmarshal(original, &reference) != nil {
				t.Fatal("fixture decode")
			}
			want, ok := oldContextBudget(&reference, protected, budget)
			got, err := budgetContext(&next, protected, budget)
			if (err == nil) != ok || ok && string(got) != string(want) {
				t.Fatalf("budget %d: got %s (%v), want %s (%v)", budget, got, err, want, ok)
			}
			if ok {
				if len(got)+1 > budget {
					t.Fatal("wire budget exceeded")
				}
				if next.OmittedMessages != r.OmittedMessages+len(r.Messages)-len(next.Messages) || next.OmittedPins != r.OmittedPins+len(r.Pins)-len(next.Pins) || next.OmittedProfiles != r.OmittedProfiles+len(r.Participants)-len(next.Participants) {
					t.Fatal("omission accounting changed")
				}
				kept := map[string]bool{}
				for _, m := range next.Messages {
					kept[m.ID] = true
				}
				for id := range protected {
					if !kept[id] {
						t.Fatal("protected message dropped")
					}
				}
			}
		}
	})
}
