package server

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func FuzzHistoryRetentionAndPagination(f *testing.F) {
	f.Add(uint8(3), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	f.Add(uint8(0), []byte{255, 0, 2, 9})
	f.Add(uint8(1), []byte{0, 0, 0, 7, 9, 13, 255, 0})
	f.Fuzz(func(t *testing.T, capacity uint8, ops []byte) {
		if len(ops) > 128 {
			t.Skip()
		}
		limit := int(capacity % 17)
		h := newHistory(limit)
		h.quotas = map[string]roomSettings{}
		model := historyModel{limit: limit, quotas: map[string]int{}}
		targets := []string{"#a", "#b", "#A", "worker"}
		for step, op := range ops {
			target := targets[int(op/4)%len(targets)]
			if op%5 == 4 {
				quota := int(op/16) % 8
				h.quotas[target] = roomSettings{HistoryLimit: quota}
				h.trimQuotas()
				model.quotas[target] = quota
				model.trim()
			} else {
				v := Message{ID: fmt.Sprintf("%032x", step+1), Seq: uint64(step + 1), From: "writer", Target: target, Body: "@worker evidence"}
				if op&16 != 0 {
					v.From = "WORKER"
				}
				if op&32 != 0 {
					v.AccountID = "account"
				}
				if op&64 != 0 {
					v.RequestID = fmt.Sprintf("request-%d", step)
				}
				if len(model.messages) > 0 && op%5 != 0 {
					parent := model.messages[int(op)%len(model.messages)]
					v.ReplyTo = parent.ID
					v.ThreadID = parent.ThreadID
					v.Target = parent.Target
					if v.ThreadID == "" {
						v.ThreadID = parent.ID
					}
					switch op % 5 {
					case 2:
						v.Supersedes = parent.ID
						v.Kind = "correct"
					case 3:
						v.Supersedes = parent.ID
						v.Kind = "retract"
					}
				}
				h.add(v)
				model.add(v)
			}
			if h.size != len(model.messages) || len(h.positions) != len(model.messages) {
				t.Fatalf("step %d: retention/index length mismatch", step)
			}
			requests := 0
			for i, want := range model.messages {
				if got := h.at(i); !reflect.DeepEqual(got, want) {
					t.Fatalf("step %d record %d: got %+v want %+v", step, i, got, want)
				}
				got, found := h.message(want.ID)
				if !found || !reflect.DeepEqual(got, want) {
					t.Fatal("ID index diverged")
				}
				if want.RequestID != "" {
					requests++
					key := want.AccountID + ":" + want.RequestID
					if want.AccountID == "" {
						key = "guest:" + strings.ToLower(want.From) + ":" + want.RequestID
					}
					at, found := h.requests[key]
					if !found || h.items[at].ID != want.ID {
						t.Fatal("request index diverged")
					}
				}
			}
			if len(h.requests) != requests {
				t.Fatal("evicted request index retained")
			}
			selectors := []string{"#a", "#A", "WORKER", "@worker", "@*", "thread:" + fmt.Sprintf("%032x", 1), "replies:" + fmt.Sprintf("%032x", 1)}
			after := []string{"", "*", fmt.Sprintf("%032x", 1)}[int(op)%3]
			if len(model.messages) > 0 && op&128 != 0 {
				after = model.messages[int(op)%len(model.messages)].ID
			}
			for _, selector := range selectors {
				count := 1 + int(op%8)
				want, status := modelSince(model.messages, selector, after, count)
				got, actual := h.since(selector, after, count)
				if actual != status || ids(got) != ids(want) {
					t.Fatalf("step %d %s after %s: got %s/%s want %s/%s", step, selector, after, ids(got), actual, ids(want), status)
				}
				for i := range got {
					if !reflect.DeepEqual(got[i], want[i]) {
						t.Fatal("history query changed original text or annotations")
					}
				}
			}
		}
	})
}
