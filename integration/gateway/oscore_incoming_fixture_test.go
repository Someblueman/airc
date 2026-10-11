//go:build darwin || linux

package gateway_test

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/core"
)

func oscoreInput(t *testing.T, f *oscoreFixture, r core.Request, size int) []byte {
	t.Helper()
	data, err := json.Marshal(frameRequest{Version: 1, CorrelationID: r.RequestID, Profile: "plain", Request: f.credentials.token(t, r, false)})
	if err != nil {
		t.Fatal(err)
	}
	if size > 0 {
		if len(data) > size {
			t.Fatal("fixture frame exceeds padded input size")
		}
		data = append(data, strings.Repeat(" ", size-len(data))...)
	}
	return data
}

func oscoreSequence(t *testing.T, path string) uint64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sequence, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return sequence
}

func resumeOSCORESequence(t *testing.T, f *oscoreFixture, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.sequence, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func noOSCOREJob(t *testing.T, f *oscoreFixture, id string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.Join(f.config.StateDir, "journal.sqlite"), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(t.Context(), "SELECT count(*) FROM jobs WHERE request_id=?", id).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial or denied native request reached durable intake", count, err)
	}
}
