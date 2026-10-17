package server

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Paging any target from any cursor through a small window, small segments
// (indexed or not) and a small read budget returns exactly the messages
// after the cursor that match, in order, as a model of all posts says.
func FuzzArchivePaging(f *testing.F) {
	f.Add(uint8(3), uint16(900), uint16(700), []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15})
	f.Add(uint8(1), uint16(300), uint16(64), []byte{9, 9, 9, 1, 1, 2, 200, 3})
	f.Fuzz(func(t *testing.T, window uint8, segment, budget uint16, ops []byte) {
		if len(ops) == 0 || len(ops) > 200 {
			t.Skip()
		}
		defer func(previous int64) { archiveBudget = previous }(archiveBudget)
		archiveBudget = 64 + int64(budget)%4096
		s := New(Config{HistoryLimit: 1 + int(window)%8})
		s.histSegment = 256 + int64(segment)%4096
		if err := s.RestoreHistory(filepath.Join(t.TempDir(), "history.jsonl")); err != nil {
			t.Fatal(err)
		}
		defer s.Shutdown(context.Background())
		rooms := []string{"#a", "#b", "#quiet"}
		nicks := []string{"bob", "carol"}
		var posted []*entry
		for i, op := range ops {
			from, target, body := nicks[int(op)%2], rooms[int(op/2)%3], fmt.Sprint("m", i)
			switch op % 7 {
			case 5:
				target = nicks[int(op/7)%2] // a direct message
			case 6:
				body = fmt.Sprintf("@%s m%d", nicks[int(op/7)%2], i)
			}
			m := post(s, from, target, body)
			posted = append(posted, archivedEntry(m))
			if op&64 != 0 {
				// Index what is sealed so far: reads mix indexed and scanned segments.
				for _, seg := range s.archive.unindexed() {
					_ = s.archive.index(seg)
				}
			}
		}
		for _, target := range []string{"#a", "#quiet", "@bob", "carol", "@*"} {
			from := int(ops[0]) % len(posted)
			var want []string
			for _, e := range posted[from+1:] {
				if matchesTarget(e, target) {
					want = append(want, e.Body)
				}
			}
			got := readAll(t, s, target, posted[from].ID)
			if !slices.Equal(got, want) {
				t.Fatalf("%s after %s:\n got %s\nwant %s", target, posted[from].Body, strings.Join(got, ","), strings.Join(want, ","))
			}
		}
	})
}
