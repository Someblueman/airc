//go:build darwin || linux

package adapter

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func directoryMailboxFixture(t *testing.T) (string, *DirectoryMailbox) {
	t.Helper()
	path := mailboxDirectory(t)
	for _, name := range []string{"inbox", "processing", "results"} {
		if err := os.Mkdir(filepath.Join(path, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	m, err := OpenDirectoryMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return path, m
}

func TestDirectoryMailboxNamedQueueAndInterruptedClaim(t *testing.T) {
	path, m := directoryMailboxFixture(t)
	for _, name := range []string{"b.json", "a.json"} {
		if err := os.WriteFile(filepath.Join(path, "inbox", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	name, body, err := m.Claim()
	if err != nil || name != "a.json" || string(body) != name {
		t.Fatal("wrong first named claim", name, err)
	}
	if other, err := OpenDirectoryMailbox(path); err == nil {
		_ = other.Close()
		t.Fatal("second directory owner admitted")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenDirectoryMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	name, body, err = m.Claim()
	if err != nil || name != "a.json" || string(body) != name {
		t.Fatal("interrupted claim not resumed", name, err)
	}
	if err := m.Publish("b.json", []byte("wrong")); err == nil {
		t.Fatal("publication did not bind claimed name")
	}
	result := bytes.Repeat([]byte("r"), MaxResponseFrameBytes)
	if err := m.Publish(name, result); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(path, "results", name))
	if err != nil || !bytes.Equal(data, result) {
		t.Fatal("named result mismatch", err)
	}
	if _, err := os.Stat(filepath.Join(path, "processing", name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published input retained", err)
	}
	name, _, err = m.Claim()
	if err != nil || name != "b.json" {
		t.Fatal("unread other result blocked queue", name, err)
	}
	if err := m.Publish(name, []byte("second")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "inbox", "a.json"), []byte("retry"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Claim(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unread same-name result overwritten", err)
	}
	if err := os.Remove(filepath.Join(path, "results", "a.json")); err != nil {
		t.Fatal(err)
	}
	name, body, err = m.Claim()
	if err != nil || name != "a.json" || string(body) != "retry" {
		t.Fatal("consumed result did not permit name reuse", err)
	}
}

func TestDirectoryMailboxResumesPublicationGap(t *testing.T) {
	path, m := directoryMailboxFixture(t)
	for _, directory := range []string{"processing", "results"} {
		if err := os.WriteFile(filepath.Join(path, directory, "a.json"), []byte(directory), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.Claim(); !errors.Is(err, ErrMailboxResultPending) {
		t.Fatal("completed unread result did not retain interrupted input", err)
	}
	if err := os.Remove(filepath.Join(path, "results", "a.json")); err != nil {
		t.Fatal(err)
	}
	name, body, err := m.Claim()
	if err != nil || name != "a.json" || string(body) != "processing" {
		t.Fatal("publication gap lost input", err)
	}
	if err := m.Publish(name, []byte("replayed")); err != nil {
		t.Fatal(err)
	}
}
