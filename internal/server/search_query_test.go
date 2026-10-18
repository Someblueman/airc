package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestParseSearchTerms(t *testing.T) {
	cases := []struct {
		query            string
		include, exclude []string
		err              string
	}{
		{query: "port", include: []string{"port"}},
		{query: "  Port   DECISION ", include: []string{"port", "decision"}},
		{query: `"exact phrase" other`, include: []string{"exact phrase", "other"}},
		{query: `deploy -staging -"dry run"`, include: []string{"deploy"}, exclude: []string{"staging", "dry run"}},
		{query: `"unclosed phrase runs on`, include: []string{"unclosed phrase runs on"}},
		{query: "- dash alone", include: []string{"-", "dash", "alone"}},
		{query: `"" empty`, include: []string{"empty"}},
		{query: "-only -excluded", err: "at least one term"},
		{query: strings.Repeat("w ", 17), err: "at most 16"},
	}
	for _, c := range cases {
		m, err := parseSearchTerms(c.query)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want %q", c.query, err, c.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", c.query, err)
			continue
		}
		var include, exclude []string
		for _, term := range m.include {
			include = append(include, term.query)
		}
		for _, term := range m.exclude {
			exclude = append(exclude, term.query)
		}
		if fmt.Sprint(include) != fmt.Sprint(c.include) || fmt.Sprint(exclude) != fmt.Sprint(c.exclude) {
			t.Errorf("%q: include %q exclude %q, want %q %q", c.query, include, exclude, c.include, c.exclude)
		}
	}
	m, _ := parseSearchTerms(`port -"port 80" decided`)
	for body, want := range map[string]bool{
		"We DECIDED the port is 6667":     true,
		"decided: support for port 6697":  true, // terms are substrings
		"decided to keep port 80 for now": false,
		"the port is open":                false,
	} {
		if got := m.matches(body); got != want {
			t.Errorf("matches(%q) = %v, want %v", body, got, want)
		}
	}
}

func TestParseSearchOptions(t *testing.T) {
	o, err := parseSearchOptions("terms,newest,since=100,until=200")
	if err != nil || !o.terms || !o.newest || o.since.Unix() != 100 || o.until.Unix() != 200 {
		t.Fatalf("options = %+v, %v", o, err)
	}
	if !o.inTime(time.Unix(100, 0)) || o.inTime(time.Unix(200, 0)) || o.inTime(time.Unix(99, 0)) {
		t.Fatal("since is inclusive and until exclusive")
	}
	if o, err := parseSearchOptions("*"); err != nil || o != (searchOptions{}) {
		t.Fatalf("* = %+v, %v", o, err)
	}
	for _, bad := range []string{"terms,terms", "since=-1", "since=x", "until=", "newest=1", "rank", "since=5,until=5", "terms,"} {
		if _, err := parseSearchOptions(bad); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

// searchWire sends one SEARCH and returns the matched bodies and the end line's
// status and cursor.
func searchWire(t *testing.T, conn net.Conn, reader *bufio.Reader, line string) (bodies []string, status, cursor string) {
	t.Helper()
	io.WriteString(conn, line+"\r\n")
	for {
		got := expectLine(t, reader, conn, " 76")
		switch {
		case strings.Contains(got, " 760 "):
			_, data, _ := strings.Cut(got, " :")
			m, err := protocol.DecodeMessageMetadata(strings.TrimSpace(data))
			if err != nil {
				t.Fatalf("760 %q: %v", got, err)
			}
			bodies = append(bodies, m.Message)
		case strings.Contains(got, " 761 "):
			fields := strings.Fields(strings.SplitN(got, " :", 2)[0])
			// :server 761 nick target status [cursor]
			status = fields[4]
			if len(fields) > 5 {
				cursor = fields[5]
			}
			return bodies, status, cursor
		}
	}
}

func TestSearchTermsOverTheWire(t *testing.T) {
	var clock atomic.Int64
	base := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	clock.Store(base.UnixNano())
	address, s := startServer(t, Config{HistoryLimit: 100, PingInterval: time.Hour, ReadTimeout: time.Hour, Now: func() time.Time { return time.Unix(0, clock.Load()) }}, nil)
	ids := map[string]string{}
	for i, body := range []string{
		"we should decide the PORT soon",
		"unrelated chatter",
		"decision: port 6667, final",
		"port 80 was the old decision",
		"bob decided on staging first",
		"Decided: port stays 6667",
	} {
		clock.Store(base.Add(time.Duration(i) * time.Hour).UnixNano())
		ids[body] = post(s, "alice", "#room", body).ID
	}
	conn, reader := registered(t, address, "reader")

	// Without options SEARCH is the literal substring it always was.
	if got, status, _ := searchWire(t, conn, reader, "SEARCH #room 50 * * :port decision"); len(got) != 0 || status != "ok" {
		t.Fatalf("literal = %q %s", got, status)
	}
	got, _, _ := searchWire(t, conn, reader, "SEARCH #room 50 * * terms :port decision")
	if strings.Join(got, "|") != "decision: port 6667, final|port 80 was the old decision" {
		t.Fatalf("terms = %q", got)
	}
	got, _, _ = searchWire(t, conn, reader, `SEARCH #room 50 * * terms :decid port -"port 80"`)
	if strings.Join(got, "|") != "we should decide the PORT soon|Decided: port stays 6667" {
		t.Fatalf("exclusion = %q", got)
	}

	// Newest first pages backwards through the window.
	got, status, cursor := searchWire(t, conn, reader, "SEARCH #room 2 * * terms,newest :port")
	if strings.Join(got, "|") != "Decided: port stays 6667|port 80 was the old decision" || status != "more" || cursor != ids["port 80 was the old decision"] {
		t.Fatalf("newest page 1 = %q %s %s", got, status, cursor)
	}
	got, status, _ = searchWire(t, conn, reader, "SEARCH #room 2 "+cursor+" * terms,newest :port")
	if strings.Join(got, "|") != "decision: port 6667, final|we should decide the PORT soon" || status != "ok" {
		t.Fatalf("newest page 2 = %q %s", got, status)
	}

	// Since is inclusive and until exclusive, in whole seconds.
	window := fmt.Sprintf("terms,since=%d,until=%d", base.Add(2*time.Hour).Unix(), base.Add(5*time.Hour).Unix())
	got, _, _ = searchWire(t, conn, reader, "SEARCH #room 50 * * "+window+" :port")
	if strings.Join(got, "|") != "decision: port 6667, final|port 80 was the old decision" {
		t.Fatalf("time window = %q", got)
	}
	got, status, _ = searchWire(t, conn, reader, fmt.Sprintf("SEARCH #room 50 * * newest,since=%d :port", base.Add(3*time.Hour).Unix()))
	if strings.Join(got, "|") != "Decided: port stays 6667|port 80 was the old decision" || status != "ok" {
		t.Fatalf("newest since = %q %s", got, status)
	}

	for _, bad := range []string{"SEARCH #room 50 * * rank :port", "SEARCH #room 50 * * terms :-port", "SEARCH #room 50 * * since=9,until=3 :port"} {
		io.WriteString(conn, bad+"\r\n")
		if line := expectLine(t, reader, conn, " 461 "); !strings.Contains(line, "SEARCH") {
			t.Fatalf("%s: %s", bad, line)
		}
	}
}

func TestSearchNewestReportsOlderMatchesInTheArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	address, s := startServer(t, Config{HistoryLimit: 4, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
		s.histSegment = 2048
		return s.RestoreHistory(path)
	})
	for i := range 40 {
		post(s, "alice", "#room", fmt.Sprint("needle ", i))
	}
	conn, reader := registered(t, address, "reader")
	got, status, _ := searchWire(t, conn, reader, "SEARCH #room 50 * * newest :needle")
	if strings.Join(got, ",") != "needle 39,needle 38,needle 37,needle 36" || status != historyArchived {
		t.Fatalf("newest over a window with an archive = %q %s", got, status)
	}
	// An oldest-first search still reaches the archive.
	_, _, cursor := searchWire(t, conn, reader, "SEARCH #room 1 * * :needle")
	if cursor == "" {
		t.Fatal("no cursor")
	}
	// Bounded by since, nothing older can match, so the archive is not owed.
	got, status, _ = searchWire(t, conn, reader, fmt.Sprintf("SEARCH #room 50 * * newest,since=%d :needle", time.Now().Add(time.Hour).Unix()))
	if len(got) != 0 || status != "ok" {
		t.Fatalf("newest since the future = %q %s", got, status)
	}
}

func TestSearchNewestYieldsTheLockAcrossLargeWindows(t *testing.T) {
	s := New(Config{HistoryLimit: 3 * readChunk})
	for i := range 3 * readChunk {
		post(s, "alice", "#room", fmt.Sprint("m", i))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accept := func(e *entry) bool { return e.Body == "m0" || e.Body == fmt.Sprint("m", 3*readChunk-1) }
	out, status, _, found := s.readBeforeLocked("#room", "*", 10, time.Time{}, accept)
	if !found || status != historyOK || len(out) != 2 || out[0].Body != fmt.Sprint("m", 3*readChunk-1) || out[1].Body != "m0" {
		t.Fatalf("chunked backward read = %d %s %v", len(out), status, found)
	}
	all, _, _, _ := s.readBeforeLocked("*", "*", 1000, time.Time{}, nil)
	if len(all) != 1000 || all[0].Body != fmt.Sprint("m", 3*readChunk-1) {
		t.Fatalf("scan selection = %d", len(all))
	}
	before, _, _, _ := s.readBeforeLocked("*", all[999].ID, 1, time.Time{}, nil)
	if len(before) != 1 || before[0].Body != fmt.Sprint("m", 3*readChunk-1001) {
		t.Fatalf("scan with a cursor = %v", before)
	}
}

func FuzzParseSearchTerms(f *testing.F) {
	for _, q := range []string{"port", `"a b" -c`, `-"x`, "- -", `""`, "a\tb\nc", strings.Repeat("-a ", 20)} {
		f.Add(q, "a b c port x")
	}
	f.Fuzz(func(t *testing.T, query, body string) {
		if len(query) > 256 || len(body) > 4096 {
			t.Skip()
		}
		m, err := parseSearchTerms(query)
		if err != nil {
			return
		}
		if len(m.include) == 0 || len(m.include)+len(m.exclude) > maxSearchTerms {
			t.Fatalf("%q parsed to %d include and %d exclude terms", query, len(m.include), len(m.exclude))
		}
		want := true
		for _, term := range m.include {
			want = want && strings.Contains(strings.ToLower(body), term.query)
		}
		for _, term := range m.exclude {
			want = want && !strings.Contains(strings.ToLower(body), term.query)
		}
		if got := m.matches(body); got != want {
			t.Fatalf("query %q body %q: got %v want %v", query, body, got, want)
		}
	})
}
