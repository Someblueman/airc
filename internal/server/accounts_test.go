package server

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

func TestRegisterCreatesOneAccountPerConnection(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	conn, reader := rawConn(t, address)
	io.WriteString(conn, "REGISTER first :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 779 ")
	io.WriteString(conn, "REGISTER second :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 437 ")
	// The limit is on creation: repeating the first registration still works,
	// and the refusal is not a failed credential.
	io.WriteString(conn, "REGISTER first :"+testAdminToken+"\r\nAUTH first :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 779 ")
	expectLine(t, reader, conn, " 779 ")
	s.mu.Lock()
	count := len(s.accounts)
	s.mu.Unlock()
	if count != 1 {
		t.Fatalf("accounts: %d", count)
	}
	createWireAccount(t, address, "second")
}

func TestAccountDeleteFreesNicknameAndRevokesOperatorRights(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	createWireAccount(t, address, "alice")
	createWireAccount(t, address, "bob")
	control, cr := wireRegister(t, address, "admin", true)
	io.WriteString(control, "ADMIN :{\"action\":\"account-list\"}\r\nOPER :"+testAdminToken+"\r\n")
	expectLine(t, cr, control, " 481 ")
	expectLine(t, cr, control, " 381 ")
	io.WriteString(control, "MODE #room +o alice\r\n")
	expectLine(t, cr, control, " MODE #room +o alice")

	io.WriteString(control, "ADMIN :{\"action\":\"account-list\"}\r\n")
	var listed []protocol.AdminResult
	for {
		line := expectLine(t, cr, control, " 77")
		command, _ := protocol.Parse(line)
		if strings.Contains(line, " 776 ") {
			break
		}
		var result protocol.AdminResult
		if err := json.Unmarshal([]byte(command.Trailing), &result); err != nil {
			t.Fatal(err)
		}
		listed = append(listed, result)
	}
	if len(listed) != 2 || listed[0].Nick != "alice" || listed[1].Nick != "bob" || !protocol.ValidMessageID(listed[0].AccountID) || listed[0].Action != "account-list" {
		t.Fatalf("list: %+v", listed)
	}

	// A live session authenticated as alice, holding an operator grant.
	op, or := rawConn(t, address)
	io.WriteString(op, "AUTH alice :"+testAdminToken+"\r\nEPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, or, op, " 001 ")
	victim, vr := wireRegister(t, address, "guest", false)
	io.WriteString(victim, "JOIN #room\r\n")
	expectLine(t, vr, victim, "JOIN #room")
	io.WriteString(op, "KICK #room nobody :probe\r\n")
	expectLine(t, or, op, " 401 ")

	// A failed write applies nothing.
	s.mu.Lock()
	original := s.accountsAt
	s.accountsAt = filepath.Join(dir, "missing", "accounts")
	s.mu.Unlock()
	io.WriteString(control, "ADMIN :{\"action\":\"account-delete\",\"nick\":\"alice\"}\r\n")
	expectLine(t, cr, control, " 437 ")
	s.mu.Lock()
	s.accountsAt = original
	_, still := s.accounts["alice"]
	s.mu.Unlock()
	if !still {
		t.Fatal("failed write removed the account")
	}

	result := adminWire(t, control, cr, protocol.AdminRequest{Action: "account-delete", Nick: "ALICE"})
	if !result.Changed || result.Nick != "alice" || result.AccountID != listed[0].AccountID {
		t.Fatalf("delete: %+v", result)
	}
	if result := adminWire(t, control, cr, protocol.AdminRequest{Action: "account-delete", Nick: "alice"}); result.Changed {
		t.Fatal("deleting an absent account reported a change")
	}

	// Nothing was disconnected, and the nickname is free for others again once
	// this session leaves; the live session just lost its account privileges.
	io.WriteString(op, "KICK #room guest :now denied\r\nPING :alive\r\n")
	expectLine(t, or, op, " 482 ")
	expectLine(t, or, op, "PONG server :alive")
	io.WriteString(control, "MODE #room\r\n")
	if line := expectLine(t, cr, control, " 324 "); strings.Contains(line, "alice") {
		t.Fatal(line)
	}
	io.WriteString(op, "QUIT\r\n")
	readUntilClosed(t, op, or)

	// The name can be claimed with a new credential and gets a new identity;
	// the old credential no longer works and the new one holds no old grant.
	other, otherReader := rawConn(t, address)
	io.WriteString(other, "AUTH alice :"+testAdminToken+"\r\n")
	expectLine(t, otherReader, other, " 498 ")
	io.WriteString(other, "REGISTER alice :"+otherToken+"\r\n")
	expectLine(t, otherReader, other, " 779 ")
	io.WriteString(other, "EPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\nKICK #room guest :no grant\r\n")
	expectLine(t, otherReader, other, " 482 ")
	s.mu.Lock()
	reissued := s.accounts["alice"].ID
	s.mu.Unlock()
	if reissued == listed[0].AccountID {
		t.Fatal("account ID was reused")
	}

	// Deletions are durable.
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	address, _ = collaborationServer(t, dir)
	again, ar := wireRegister(t, address, "again", true)
	io.WriteString(again, "OPER :"+testAdminToken+"\r\nADMIN :{\"action\":\"account-delete\",\"nick\":\"bob\"}\r\n")
	expectLine(t, ar, again, " 381 ")
	expectLine(t, ar, again, "\"changed\":true")
	io.WriteString(again, "ADMIN :{\"action\":\"account-list\"}\r\n")
	if line := expectLine(t, ar, again, " 775 "); !strings.Contains(line, "alice") || !strings.Contains(line, reissued) {
		t.Fatal(line)
	}
	if line := expectLine(t, ar, again, " 776 "); line == "" {
		t.Fatal("list not terminated")
	}
}

func TestAccountAdminRequestValidation(t *testing.T) {
	address, _ := collaborationServer(t, t.TempDir())
	control, cr := wireRegister(t, address, "admin", true)
	io.WriteString(control, "OPER :"+testAdminToken+"\r\n")
	expectLine(t, cr, control, " 381 ")
	for _, body := range []string{
		`{"action":"account-delete"}`,
		`{"action":"account-delete","nick":"bad nick"}`,
		`{"action":"account-delete","nick":"alice","scope":"#room"}`,
		`{"action":"account-delete","nick":"alice","reason":"x"}`,
		`{"action":"account-delete","nick":"alice","seconds":5}`,
		`{"action":"account-list","nick":"alice"}`,
		`{"action":"account-list","scope":"#room"}`,
	} {
		io.WriteString(control, "ADMIN :"+body+"\r\n")
		expectLine(t, cr, control, " 461 ")
	}
	plain, _ := adminWireServer(t)
	conn, reader := wireRegister(t, plain, "admin", true)
	io.WriteString(conn, "OPER :"+testAdminToken+"\r\nADMIN :{\"action\":\"account-list\"}\r\nADMIN :{\"action\":\"account-delete\",\"nick\":\"x\"}\r\n")
	expectLine(t, reader, conn, " 381 ")
	expectLine(t, reader, conn, " 437 ")
	expectLine(t, reader, conn, " 437 ")
}

func TestAccountCreationIsLimitedPerRemoteAddressOverTime(t *testing.T) {
	now := time.Now()
	s := New(Config{Now: func() time.Time { return now }})
	s.mu.Lock()
	defer s.mu.Unlock()
	for range maxRegistrationsPerAddress {
		if !s.registrationAllowedLocked("203.0.113.7") {
			t.Fatal("refused before the limit")
		}
		s.noteRegistrationLocked("203.0.113.7")
	}
	if s.registrationAllowedLocked("203.0.113.7") {
		t.Fatal("an address exceeded its account creation limit by reconnecting")
	}
	if !s.registrationAllowedLocked("203.0.113.8") || !s.registrationAllowedLocked("") {
		t.Fatal("another address, or a local peer, was limited")
	}
	now = now.Add(registrationWindow + time.Second)
	if !s.registrationAllowedLocked("203.0.113.7") || len(s.registrations) != 0 {
		t.Fatalf("the limit did not lapse with the window: %v", s.registrations)
	}
}

// TestDirectMessageRecordsRecipientAccount stamps a direct message to a
// registered nickname with the account that owned it, and leaves a message
// to a guest nickname unstamped: nothing proves who received that one.
func TestDirectMessageRecordsRecipientAccount(t *testing.T) {
	address, s := collaborationServer(t, t.TempDir())
	createWireAccount(t, address, "owner")
	sender, sr := wireRegister(t, address, "sender", false)
	io.WriteString(sender, "PRIVMSG owner :to the account\r\nPRIVMSG drifter :to a guest name\r\n")
	expectLine(t, sr, sender, " 762 ")
	expectLine(t, sr, sender, " 762 ")
	s.mu.Lock()
	account := s.accounts[nickKey("owner")].ID
	owned := s.history.recent("@owner", 1)
	guest := s.history.recent("@drifter", 1)
	s.mu.Unlock()
	if len(owned) != 1 || owned[0].RecipientAccountID != account || !protocol.ValidMessageID(account) {
		t.Fatalf("message to a registered nickname: %+v (account %s)", owned, account)
	}
	if len(guest) != 1 || guest[0].RecipientAccountID != "" {
		t.Fatalf("message to a guest nickname: %+v", guest)
	}
}
