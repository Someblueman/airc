package server

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
)

// A room's charter is set by an operator or an admin, bounded, kept in the
// chat snapshot across a restart, and read by anyone with the room action.
func TestRoomCharterAuthorisationLimitsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	createWireAccount(t, address, "alice")
	admin, ar := wireRegister(t, address, "admin", true)
	io.WriteString(admin, "OPER :"+testAdminToken+"\r\nMODE #room +o alice\r\n")
	expectLine(t, ar, admin, " MODE #room +o alice")
	room := func(charter *string) string {
		data, _ := json.Marshal(protocol.ChatRequest{Action: "room", Target: "#room", Seconds: -1, Limit: -1, Charter: charter})
		return "CHAT :" + string(data) + "\r\n"
	}
	text := "Be kind.\nOne thread per task."

	// A guest and a plain account are refused with the one message.
	guest, gr := wireRegister(t, address, "guest", true)
	io.WriteString(guest, room(&text))
	if line := expectLine(t, gr, guest, " 481 "); !strings.Contains(line, "only a room operator or an admin may set the charter") {
		t.Fatal(line)
	}
	createWireAccount(t, address, "bob")
	bob, br := rawConn(t, address)
	io.WriteString(bob, "AUTH bob :"+testAdminToken+"\r\nEPHEMERAL\r\nNICK bob\r\nUSER u 0 * :u\r\n")
	expectLine(t, br, bob, " 001 ")
	io.WriteString(bob, room(&text))
	expectLine(t, br, bob, " 481 ")

	// The operator sets it; a member is told it changed; anyone reads it.
	member, mr := wireRegister(t, address, "member", false)
	io.WriteString(member, "JOIN #room\r\n")
	expectLine(t, mr, member, " 366 ")
	op, or := rawConn(t, address)
	io.WriteString(op, "AUTH alice :"+testAdminToken+"\r\nEPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, or, op, " 001 ")
	io.WriteString(op, room(&text))
	if line := expectLine(t, or, op, " 777 "); !strings.Contains(line, `"charter":"Be kind.\nOne thread per task."`) {
		t.Fatal(line)
	}
	if line := expectLine(t, mr, member, " 780 "); !strings.Contains(line, `"action":"charter"`) || !strings.Contains(line, `"from":"alice"`) {
		t.Fatal(line)
	}
	io.WriteString(guest, room(nil))
	if line := expectLine(t, gr, guest, " 777 "); !strings.Contains(line, `"charter":"Be kind.`) {
		t.Fatal(line)
	}

	// The limit holds, and an invalid charter changes nothing.
	long := strings.Repeat("x", protocol.MaxCharterBytes+1)
	io.WriteString(admin, room(&long))
	if line := expectLine(t, ar, admin, " 461 "); !strings.Contains(line, "at most 4096 bytes") {
		t.Fatal(line)
	}
	exact := strings.Repeat("y", protocol.MaxCharterBytes)
	io.WriteString(admin, room(&exact))
	expectLine(t, ar, admin, " 778 ")

	// The snapshot keeps it across a restart; clearing drops the room's
	// settings when nothing else is set.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	var saved chatState
	data, err := os.ReadFile(filepath.Join(dir, "chat"))
	if err != nil || json.Unmarshal(data, &saved) != nil || saved.Rooms["#room"].Charter != exact {
		t.Fatalf("snapshot %v: %s", err, data)
	}
	address, s = collaborationServer(t, dir)
	reader, rr := wireRegister(t, address, "reader", true)
	io.WriteString(reader, room(nil))
	if line := expectLine(t, rr, reader, " 777 "); !strings.Contains(line, `"charter":"`+exact[:16]) {
		t.Fatal(line)
	}
	admin, ar = wireRegister(t, address, "admin", true)
	empty := ""
	io.WriteString(admin, "OPER :"+testAdminToken+"\r\n"+room(&empty))
	if line := expectLine(t, ar, admin, " 777 "); strings.Contains(line, "charter") {
		t.Fatal(line)
	}
	s.mu.Lock()
	_, kept := s.chat.Rooms["#room"]
	s.mu.Unlock()
	if kept {
		t.Fatal("a room with nothing set stayed in the snapshot")
	}

	// A snapshot holding an oversized charter is refused.
	bad := newChatState()
	bad.Rooms["#room"] = roomSettings{Charter: long}
	data, _ = json.Marshal(bad)
	path := filepath.Join(t.TempDir(), "chat")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(Config{HistoryLimit: 64}).RestoreChat(path); err == nil || !strings.Contains(err.Error(), "invalid room settings") {
		t.Fatalf("oversized charter loaded: %v", err)
	}
}
