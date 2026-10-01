package server

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

const testAdminToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func adminWireServer(t *testing.T) (string, *Server) {
	t.Helper()
	s := New(Config{HistoryLimit: 32, ReadTimeout: time.Hour, PingInterval: time.Hour, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err := s.EnableAdmin(testAdminToken); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.Serve(l)
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	return l.Addr().String(), s
}

func adminWire(t *testing.T, conn net.Conn, reader *bufio.Reader, request protocol.AdminRequest) protocol.AdminResult {
	t.Helper()
	data, _ := json.Marshal(request)
	if _, err := io.WriteString(conn, "ADMIN :"+string(data)+"\r\n"); err != nil {
		t.Fatal(err)
	}
	line := expectLine(t, reader, conn, " 775 ")
	command, _ := protocol.Parse(line)
	var result protocol.AdminResult
	if err := json.Unmarshal([]byte(command.Trailing), &result); err != nil {
		t.Fatal(err)
	}
	expectLine(t, reader, conn, " 776 ")
	return result
}

func wireRegister(t *testing.T, address, nick string, ephemeral bool) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, reader := rawConn(t, address)
	if ephemeral {
		io.WriteString(conn, "EPHEMERAL\r\n")
	}
	io.WriteString(conn, "NICK "+nick+"\r\nUSER u 0 * :u\r\n")
	expectLine(t, reader, conn, " 001 ")
	return conn, reader
}

func TestAdminAuthenticationCannotBeClaimedByNickname(t *testing.T) {
	address, _ := adminWireServer(t)
	conn, reader := wireRegister(t, address, "sws", true)
	io.WriteString(conn, "ADMIN :{\"action\":\"ban\",\"nick\":\"bot\"}\r\nOPER :wrong\r\n")
	expectLine(t, reader, conn, " 481 ")
	expectLine(t, reader, conn, " 464 ")
	io.WriteString(conn, "OPER :"+testAdminToken+"\r\n")
	expectLine(t, reader, conn, " 381 ")
	for _, body := range []string{`null`, `{"action":"list","extra":1}`, `{"action":"list"} {}`, `{"action":"ban","nick":"bot","seconds":2592001}`} {
		io.WriteString(conn, "ADMIN :"+body+"\r\n")
		expectLine(t, reader, conn, " 461 ")
	}
	adminWire(t, conn, reader, protocol.AdminRequest{Action: "mute", Nick: "bot"})
	adminWire(t, conn, reader, protocol.AdminRequest{Action: "mute", Nick: "sws", Reason: strings.Repeat("<", 400)})
	adminWire(t, conn, reader, protocol.AdminRequest{Action: "unmute", Nick: "sws"})
	io.WriteString(conn, "OPER :wrong\r\nADMIN :{\"action\":\"list\"}\r\n")
	expectLine(t, reader, conn, " 464 ")
	expectLine(t, reader, conn, " 481 ")
	other, otherReader := wireRegister(t, address, "sws", true)
	io.WriteString(other, "ADMIN :{\"action\":\"list\"}\r\n")
	expectLine(t, otherReader, other, " 481 ")
	bot, botReader := wireRegister(t, address, "bot", true)
	for _, line := range []string{"NOTICE #room :blocked", "PRESENCE thinking 60", "PROFILE :{\"about\":\"blocked\"}"} {
		io.WriteString(bot, line+"\r\n")
		expectLine(t, botReader, bot, " 485 ")
	}
}

func TestRoomBanBlocksJoinObserveAndThreadSubscriptions(t *testing.T) {
	address, s := adminWireServer(t)
	control, cr := wireRegister(t, address, "operator", true)
	io.WriteString(control, "OPER :"+testAdminToken+"\r\nPRIVMSG #room :root\r\n")
	expectLine(t, cr, control, " 381 ")
	line := expectLine(t, cr, control, " 762 ")
	parsed, _ := protocol.Parse(line)
	message, _ := protocol.DecodeMessageMetadata(parsed.Trailing)
	adminWire(t, control, cr, protocol.AdminRequest{Action: "ban", Nick: "bot", Scope: "#room"})
	bot, br := wireRegister(t, address, "bot", true)
	for _, target := range []string{"#room", "thread:" + message.ID, "replies:" + message.ID} {
		io.WriteString(bot, "OBSERVE "+target+"\r\n")
		expectLine(t, br, bot, " 474 ")
	}
	persistent, pr := wireRegister(t, address, "BOT", false)
	io.WriteString(persistent, "JOIN #room\r\nJOIN #other\r\n")
	expectLine(t, pr, persistent, " 474 ")
	expectLine(t, pr, persistent, "JOIN #other")
	// Inbox mentions must not provide a back door into a banned live room.
	io.WriteString(bot, "OBSERVE @bot\r\n")
	expectLine(t, br, bot, " 765 ")
	io.WriteString(control, "PRIVMSG #room :@bot forbidden\r\nPRIVMSG #other :@bot allowed\r\n")
	expectLine(t, cr, control, " 762 ")
	expectLine(t, cr, control, " 762 ")
	got := expectLine(t, br, bot, "PRIVMSG")
	if !strings.Contains(got, "#other") {
		t.Fatal(got)
	}
	adminWire(t, control, cr, protocol.AdminRequest{Action: "ban", Nick: "blocked"})
	io.WriteString(persistent, "NICK blocked\r\n")
	expectLine(t, pr, persistent, " 465 ")
	s.mu.Lock()
	if _, claimed := s.nicks["blocked"]; claimed {
		t.Error("banned nickname claimed")
	}
	s.mu.Unlock()
}
