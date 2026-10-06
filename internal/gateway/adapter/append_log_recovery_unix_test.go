//go:build darwin || linux

package adapter

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppendLogPublicationGapAndCompletedClaimCleanup(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(map[bool]string{false: "response_before_cursor", true: "cursor_before_cleanup"}[committed], func(t *testing.T) {
			path, m := appendMailboxFixture(t, "first\nsecond\n")
			first, err := m.Claim()
			if err != nil {
				t.Fatal(err)
			}
			if err := m.stage("response.json", []byte("initial result")); err != nil {
				t.Fatal(err)
			}
			if committed {
				data, err := json.Marshal(appendLogCheckpoint{Version: 1, Cursor: first.Next})
				if err != nil {
					t.Fatal(err)
				}
				if err := m.stage("cursor.json", data); err != nil {
					t.Fatal(err)
				}
			}
			if err := m.Close(); err != nil {
				t.Fatal(err)
			}
			m, err = OpenAppendLogMailbox(path)
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			if _, err := m.Claim(); !errors.Is(err, ErrMailboxResultPending) {
				t.Fatal("unread interrupted response ignored", err)
			}
			if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
				t.Fatal(err)
			}
			resumed, err := m.Claim()
			if err != nil {
				t.Fatal(err)
			}
			if !committed {
				if string(resumed.Body) != "first" || resumed.Next != first.Next {
					t.Fatal("uncommitted result skipped retained intent", resumed)
				}
				if err := m.Publish([]byte("replayed result")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
					t.Fatal(err)
				}
				resumed, err = m.Claim()
				if err != nil {
					t.Fatal(err)
				}
			}
			if string(resumed.Body) != "second" {
				t.Fatal("completed checkpoint replayed old input", resumed)
			}
		})
	}
}

func TestAppendLogMailboxInvalidStateAndUnsafeTemporaryRefusal(t *testing.T) {
	for _, kind := range []string{"checkpoint", "claim", "transition", "temporary"} {
		t.Run(kind, func(t *testing.T) {
			path, m := appendMailboxFixture(t, "body\n")
			if kind == "checkpoint" {
				if err := os.WriteFile(filepath.Join(path, "cursor.json"), []byte(`{"version":2}`), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "claim" {
				if err := os.WriteFile(filepath.Join(path, "claim.json"), []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			} else if kind == "transition" {
				if _, err := m.Claim(); err != nil {
					t.Fatal(err)
				}
				claim, err := m.claim()
				if err != nil {
					t.Fatal(err)
				}
				claim.Next.Offset++
				data, err := json.Marshal(claim)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, "claim.json"), data, 0600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(filepath.Join(path, "claim.json.tmp"), []byte("partial"), 0644); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Claim(); err == nil {
				t.Fatal("invalid durable state accepted", kind)
			}
		})
	}
}

func TestAppendLogInterruptedClaimLinkReopens(t *testing.T) {
	path, m := appendMailboxFixture(t, "retained\n")
	first, err := m.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(path, "claim.json"), filepath.Join(path, "claim.json.tmp")); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenAppendLogMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	got, err := m.Claim()
	if err != nil || string(got.Body) != string(first.Body) || got.Next != first.Next {
		t.Fatal("interrupted claim publication lost replay", got, err)
	}
	if _, err := os.Lstat(filepath.Join(path, "claim.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("temporary alias retained", err)
	}
	if err := m.Publish([]byte("result")); err != nil {
		t.Fatal(err)
	}
}
