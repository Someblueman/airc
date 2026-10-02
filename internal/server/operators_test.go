package server

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestChannelOperatorsPersistAndKickOnlyOneRoom(t *testing.T) {
	dir := t.TempDir()
	address, s := collaborationServer(t, dir)
	createWireAccount(t, address, "alice")
	admin, ar := wireRegister(t, address, "admin", true)
	io.WriteString(admin, "OPER :"+testAdminToken+"\r\nMODE #room +o alice\r\n")
	expectLine(t, ar, admin, " MODE #room +o alice")
	op, or := rawConn(t, address)
	io.WriteString(op, "AUTH alice :"+testAdminToken+"\r\nEPHEMERAL\r\nNICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, or, op, " 001 ")
	victim, vr := wireRegister(t, address, "guest", false)
	io.WriteString(victim, "JOIN #room,#other\r\nMODE #room +o alice\r\n")
	expectLine(t, vr, victim, " 482 ")
	io.WriteString(op, "KICK #other guest :denied\r\nKICK #room guest :quiet\r\n")
	expectLine(t, or, op, " 482 ")
	expectLine(t, or, op, " KICK #room guest :quiet")
	expectLine(t, vr, victim, " KICK #room guest :quiet")
	io.WriteString(victim, "PRIVMSG #room :denied\r\nPRIVMSG #other :still here\r\n")
	expectLine(t, vr, victim, " 403 ")
	expectLine(t, vr, victim, "PRIVMSG #other :still here")
	// Failed durable writes must not apply a revocation.
	s.mu.Lock()
	original := s.chatAt
	s.chatAt = filepath.Join(dir, "missing", "chat")
	s.mu.Unlock()
	io.WriteString(op, "MODE #room -o alice\r\n")
	expectLine(t, or, op, " 437 ")
	s.mu.Lock()
	s.chatAt = original
	s.mu.Unlock()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	address, _ = collaborationServer(t, dir)
	c, r := wireRegister(t, address, "reader", true)
	io.WriteString(c, "MODE #room\r\n")
	if line := expectLine(t, r, c, " 783 "); !strings.Contains(line, "#room alice") {
		t.Fatal(line)
	}
	// A registered nickname cannot be impersonated to recover those rights.
	g, gr := rawConn(t, address)
	io.WriteString(g, "NICK alice\r\nUSER u 0 * :u\r\n")
	expectLine(t, gr, g, " 498 ")
}
