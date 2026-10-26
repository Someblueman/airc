//go:build darwin || linux

package adapter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileMailboxPreservesExistingResultAndResumesTemporaryWrite(t *testing.T) {
	path := mailboxDirectory(t)
	m, err := OpenFileMailbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for name, data := range map[string]string{"processing.json": "intent", "response.json": "previous", "response.tmp": "partial"} {
		if err := os.WriteFile(filepath.Join(path, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Publish([]byte("new")); err == nil {
		t.Fatal("unread result overwritten")
	}
	data, err := os.ReadFile(filepath.Join(path, "response.json"))
	if err != nil || string(data) != "previous" {
		t.Fatal("existing result changed", err)
	}
	if err := os.Remove(filepath.Join(path, "response.json")); err != nil {
		t.Fatal(err)
	}
	if err := m.Publish([]byte("new")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(filepath.Join(path, "response.json"))
	if err != nil || string(data) != "new" {
		t.Fatal("temporary write not recovered", err)
	}
}
