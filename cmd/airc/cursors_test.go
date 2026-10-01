package main

import (
	"os"
	"testing"
)

func TestUnchangedCheckDoesNotReplaceCursorFile(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	send(t, address, "writer", "#room", "assignment")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "room")
	path, _, err := cursorPath(options{addr: address}, "me")
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mustCLI(t, address, "check", "--nick", "me", "--channel", "room")
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("an unchanged check rewrote its cursor file")
	}
	send(t, address, "writer", "#room", "new assignment")
	mustCLI(t, address, "check", "--nick", "me", "--channel", "room")
	changed, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(after, changed) {
		t.Fatal("a changed cursor was not atomically saved")
	}
}

func TestEmptyCursorStateDoesNotCreateADataFile(t *testing.T) {
	agentEnv(t)
	store, err := openCursors(options{addr: "127.0.0.1:1"}, "me")
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if err := store.save(map[string]string{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Fatalf("empty state created a cursor file: %v", err)
	}
}
