//go:build darwin || linux

package adapter

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func appendMailboxFixture(t *testing.T, body string) (string, *AppendLogMailbox) {
	t.Helper()
	path := mailboxDirectory(t)
	if err := os.Mkdir(filepath.Join(path, "logs"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(1)), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := OpenAppendLogMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return path, m
}

func TestAppendLogMailboxRetainedClaimResultsCheckpointAndRotation(t *testing.T) {
	path, m := appendMailboxFixture(t, "first\nsecond\n\n")
	first, err := m.Claim()
	if err != nil || string(first.Body) != "first" {
		t.Fatal(first, err)
	}
	if _, err := os.Stat(filepath.Join(path, "cursor.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("claim advanced checkpoint", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenAppendLogMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	replay, err := m.Claim()
	if err != nil || !bytes.Equal(first.Body, replay.Body) || first.Next != replay.Next {
		t.Fatal("reopen changed retained intent", replay, err)
	}
	result := bytes.Repeat([]byte("r"), MaxResponseFrameBytes)
	if err := m.Publish(result); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(path, "response.json"))
	if err != nil || !bytes.Equal(result, stored) {
		t.Fatal("wrong complete result", err)
	}
	if _, err := m.Claim(); !errors.Is(err, ErrMailboxResultPending) {
		t.Fatal("unread result did not stop intake", err)
	}
	if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
		t.Fatal(err)
	}
	second, err := m.Claim()
	if err != nil || string(second.Body) != "second" || second.Next.Offset != 13 {
		t.Fatal(second, err)
	}
	if err := m.Publish(append(result, 'x')); err == nil {
		t.Fatal("oversized result accepted")
	}
	if err := m.Publish(nil); err == nil {
		t.Fatal("empty non-seal response accepted")
	}
	cursor, err := m.cursor()
	if err != nil || cursor != first.Next {
		t.Fatal("failed publication advanced cursor", cursor, err)
	}
	if err := m.Publish([]byte("second result")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
		t.Fatal(err)
	}
	seal, err := m.Claim()
	if err != nil || !seal.Next.Sealed || len(seal.Body) != 0 {
		t.Fatal(seal, err)
	}
	if err := m.Publish([]byte("invalid seal result")); err == nil {
		t.Fatal("seal published content")
	}
	if err := m.Publish(nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "logs", appendLogName(1))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "logs", appendLogName(2)), []byte("next\n"), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := m.Claim()
	if err != nil || string(next.Body) != "next" || next.Next.Generation != 2 {
		t.Fatal(next, err)
	}
}

func TestAppendLogMailboxResumeDoesNotRequireLostSource(t *testing.T) {
	path, m := appendMailboxFixture(t, "retained\n")
	record, err := m.Claim()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "logs", appendLogName(1))); err != nil {
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
	resumed, err := m.Claim()
	if err != nil || !bytes.Equal(record.Body, resumed.Body) || record.Next != resumed.Next {
		t.Fatal("lost source changed retained claim", resumed, err)
	}
	if err := m.Publish([]byte("result")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Claim(); err == nil {
		t.Fatal("missing unsealed source allowed false continuity")
	}
}
