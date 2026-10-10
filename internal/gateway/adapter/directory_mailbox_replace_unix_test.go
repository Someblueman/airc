//go:build darwin || linux

package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDirectoryMailboxClaimMatchesAtomicProducerReplacement(t *testing.T) {
	path, m := directoryMailboxFixture(t)
	inbox := filepath.Join(path, "inbox", "a.json")
	if err := os.WriteFile(inbox, []byte("initial"), 0600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan error, 1)
	var once sync.Once
	finish := func() {
		once.Do(func() {
			close(stop)
			if err := <-done; err != nil {
				t.Error(err)
			}
		})
	}
	t.Cleanup(finish)
	go func() {
		temporary := filepath.Join(path, "producer.tmp")
		for i := 0; ; i++ {
			select {
			case <-stop:
				done <- nil
				return
			default:
			}
			if err := os.WriteFile(temporary, []byte(fmt.Sprintf("replacement-%d", i)), 0600); err != nil {
				done <- err
				return
			}
			if err := os.Rename(temporary, inbox); err != nil {
				done <- err
				return
			}
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for i := 0; i < 100; {
		if time.Now().After(deadline) {
			t.Fatal("atomic replacement did not allow 100 claims within deadline")
		}
		name, body, err := m.Claim()
		// Replacement may unlink the inode opened for validation, which is
		// correctly refused before rename. A successful claim must always
		// return the retained inode's contents.
		if errors.Is(err, os.ErrNotExist) || err != nil && err.Error() == "file mailbox input must be bounded private regular file" {
			if _, statErr := os.Stat(filepath.Join(path, "processing", "a.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("refused validation left a claimed file", statErr)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		retained, err := os.ReadFile(filepath.Join(path, "processing", name))
		if err != nil || !bytes.Equal(body, retained) {
			t.Fatal("claimed bytes differ from retained replay intent", err)
		}
		if err := m.Publish(name, []byte("result")); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(path, "results", name)); err != nil {
			t.Fatal(err)
		}
		i++
	}
	finish()
}
