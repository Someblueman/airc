package server

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

func FuzzCheckCursorPrefixes(f *testing.F) {
	f.Add(uint8(15), []byte{0, 0, 0, 0, 1, 2}, uint8(4), uint8(0))
	f.Add(uint8(8), []byte{0, 1, 2, 8, 9, 10, 4, 5, 6, 0, 1, 2}, uint8(1), uint8(2))
	f.Add(uint8(1), []byte{0, 8, 10, 2}, uint8(4), uint8(1))
	f.Fuzz(func(t *testing.T, capacity uint8, ops []byte, pageSize, budget uint8) {
		if len(ops) > 48 {
			t.Skip()
		}
		limit := 1 + int(capacity%16)
		s := New(Config{HistoryLimit: limit})
		var retained []Message
		for i, op := range ops {
			v := Message{ID: fmt.Sprintf("%032x", i+1), Seq: uint64(i + 1), From: "writer", Target: []string{"#a", "#b", "worker"}[int(op)%3], Body: fmt.Sprintf("record %d é🐈\n", i)}
			if op&4 != 0 {
				v.From = "WORKER"
			}
			if op&8 != 0 {
				v.Body += "@worker evidence"
			}
			s.history.add(v)
			retained = append(retained, v)
			if len(retained) > limit {
				retained = retained[1:]
			}
		}
		after := []string{"*", fmt.Sprintf("%032x", 0), "*"}[int(capacity)%3]
		if capacity%3 == 2 && len(retained) > 0 {
			after = retained[len(retained)/2].ID
		}
		positions := map[string]int{}
		byID := map[string]Message{}
		for i, v := range retained {
			positions[v.ID] = i
			byID[v.ID] = v
		}
		start := 0
		if at, ok := positions[after]; ok {
			start = at + 1
		}
		includeOwn := budget&128 != 0
		request := protocol.CheckRequest{MaxMessages: 1 + int(budget%8), IncludeOwn: includeOwn}
		selectors := []string{"#a", "#b", "@worker", "@*"}
		remaining := make([][]Message, len(selectors))
		for i, target := range selectors {
			request.Targets = append(request.Targets, protocol.CheckTarget{Target: target, After: after, Limit: 1 + (int(pageSize)+i)%5})
			for _, v := range retained[start:] {
				if modelMatches(v, target) && (includeOwn || v.From != "WORKER") {
					remaining[i] = append(remaining[i], v)
				}
			}
		}
		for round := 0; round <= 4*len(retained)+1; round++ {
			actor := &session{client: Client{Nick: "worker"}, out: make(chan string, 128), done: make(chan struct{})}
			data, _ := json.Marshal(request)
			s.checkLocked(actor, protocol.Command{Trailing: string(data)})
			pages := map[string]protocol.CheckEntry{}
			received := make([][]string, len(selectors))
			seen := map[string]bool{}
			last := -1
			ended := false
			for len(actor.out) > 0 {
				line := <-actor.out
				wire, err := protocol.Parse(line)
				if err != nil {
					t.Fatal(err)
				}
				if wire.Name == "785" {
					ended = true
					continue
				}
				if wire.Name != "784" {
					t.Fatal("CHECK rejected generated valid input", line)
				}
				var e protocol.CheckEntry
				if err := json.Unmarshal([]byte(wire.Trailing), &e); err != nil {
					t.Fatal(err)
				}
				switch e.Kind {
				case "page":
					if _, dup := pages[e.Target]; dup {
						t.Fatal("duplicate page")
					}
					pages[e.Target] = e
				case "message":
					if e.Message == nil {
						t.Fatal("missing body")
					}
					id := e.Message.ID
					v, ok := byID[id]
					if !ok || seen[id] || positions[id] <= last || e.Message.Message != v.Body || len(e.Targets) == 0 {
						t.Fatal("message ordering, uniqueness or original body violated", e)
					}
					seen[id] = true
					last = positions[id]
					associated := map[int]bool{}
					for _, index := range e.Targets {
						if index < 0 || index >= len(selectors) || associated[index] {
							t.Fatal("invalid association", e)
						}
						associated[index] = true
						received[index] = append(received[index], id)
					}
				default:
					t.Fatal("unexpected entry", e)
				}
			}
			if !ended || len(pages) != len(selectors) || len(seen) > request.MaxMessages {
				t.Fatal("missing terminator/page or exceeded message budget")
			}
			done := true
			progress := 0
			for i, target := range selectors {
				p := pages[target]
				old := request.Targets[i].After
				_, known := positions[old]
				if p.Gap != (old != "*" && !known) {
					t.Fatal("incorrect retention gap", p)
				}
				if len(received[i]) > request.Targets[i].Limit || len(received[i]) > len(remaining[i]) {
					t.Fatal("target limit exceeded")
				}
				for j, id := range received[i] {
					if remaining[i][j].ID != id {
						t.Fatal("page is not the eligible contiguous prefix")
					}
				}
				remaining[i] = remaining[i][len(received[i]):]
				progress += len(received[i])
				if p.Status == "ok" {
					if len(remaining[i]) != 0 {
						t.Fatal("hidden omission")
					}
				} else if p.Status != "more" || len(remaining[i]) == 0 {
					t.Fatal("incorrect continuation status", p)
				}
				if len(remaining[i]) > 0 {
					done = false
					if at, ok := positions[p.Cursor]; ok && at >= positions[remaining[i][0].ID] {
						t.Fatal("cursor advanced past a deferred message")
					}
					if p.Cursor != old {
						if _, ok := positions[p.Cursor]; !ok {
							t.Fatal("invented cursor")
						}
					}
				}
				request.Targets[i].After = p.Cursor
			}
			if done {
				return
			}
			if progress == 0 {
				t.Fatal("nonempty pages made no progress")
			}
		}
		t.Fatal("pagination did not drain")
	})
}
