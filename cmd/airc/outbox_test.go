package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestOutboxBoundsPreserveUncertainEntries(t *testing.T) {
	agentEnv(t)
	b, err := openOutbox(options{addr: "127.0.0.1:1234", nick: "writer"})
	if err != nil {
		t.Fatal(err)
	}
	defer b.close()
	for i := 0; i < maxOutboxEntries; i++ {
		b.Entries = append(b.Entries, outboundMessage{RequestID: fmt.Sprint(i), Target: "#room", Body: "pending"})
	}
	if _, err := b.add("#room", "", "new", "extra"); err == nil {
		t.Fatal("full uncertain outbox accepted a new send")
	}
	if len(b.Entries) != maxOutboxEntries || b.find("0") == nil {
		t.Fatal("uncertain entry was discarded")
	}
	b.Entries[20].Result = &sendResult{MessageEvent: &irc.MessageEvent{ID: "confirmed", ChatMetadata: irc.ChatMetadata{RequestID: "20"}, Message: "pending"}}
	if _, err := b.add("#room", "", "new", "extra"); err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != maxOutboxEntries || b.find("20") != nil || b.find("0") == nil || b.find("extra") == nil {
		t.Fatal("wrong entry was evicted")
	}
	info, err := os.Stat(b.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("outbox is not owner-only", err)
	}
	if _, err := b.add("#room", "", "different", "extra"); err == nil {
		t.Fatal("request ID content changed")
	}
}

func TestDamagedStateFilesAreSetAsideInsteadOfBlockingTheAgent(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "hello")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "room")
	mustCLI(t, address, "send", "--nick", "me", "--channel", "room", "--message", "mine")
	dir := os.Getenv("AIRC_STATE_DIR")
	damaged := 0
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			damaged++
			if err := os.WriteFile(filepath.Join(dir, entry.Name()), []byte("{truncated"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if damaged < 2 {
		t.Fatalf("expected a cursor and an outbox file, found %d", damaged)
	}
	out, stderr, err := cli(t, address, "check", "--nick", "me", "--channel", "room")
	if err != nil || !strings.Contains(out, "hello") || !strings.Contains(stderr, "moved to") {
		t.Fatalf("check after damage: %q %q %v", out, stderr, err)
	}
	mustCLI(t, address, "send", "--nick", "me", "--channel", "room", "--message", "still works")
	kept, _ := filepath.Glob(filepath.Join(dir, "*.bad"))
	if len(kept) != 2 { // this nickname's cursors and outbox; other identities were not opened
		t.Fatalf("damaged files kept for inspection: %v", kept)
	}
}

func TestConcurrentSendsOnOneNicknameAllSucceed(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	errs := make(chan error, 8)
	for i := range cap(errs) {
		go func() {
			_, stderr, err := cli(t, address, "send", "--nick", "busy", "--channel", "room", "--message", fmt.Sprint("parallel ", i))
			if err != nil {
				err = fmt.Errorf("%w: %s", err, stderr)
			}
			errs <- err
		}()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Count(mustCLI(t, address, "history", "#room"), "parallel "); got != cap(errs) {
		t.Fatalf("%d of %d parallel sends were retained", got, cap(errs))
	}
}
