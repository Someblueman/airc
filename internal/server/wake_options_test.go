package server

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testwait"
)

// The wake options are stored with the registration and shown in 787;
// mentions-only skips the steps of a handoff the account offered; quiet
// hours hold the news, which goes as one notice when the window ends. The
// clock is cfg.Now, so nothing sleeps through a window.
func TestWakeOptionsStoredAndQuietHoursDeferNotices(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff"
	key, _ := hex.DecodeString(secret)
	receiver := &wakeReceiver{t: t, secret: key, notices: make(chan wakeNotice, 16)}
	hook := httptest.NewServer(receiver)
	defer hook.Close()
	dir := t.TempDir()
	var mu sync.Mutex
	now := time.Date(2026, 10, 26, 23, 30, 0, 0, time.Local) // inside 22:00-08:00
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	address, srv := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, ReadTimeout: time.Hour, Now: clock}, func(s *Server) error {
		for _, err := range []error{s.EnableAdmin(testAdminToken), s.RestoreAccounts(filepath.Join(dir, "accounts")), s.RestoreChat(filepath.Join(dir, "chat")), s.EnableWake([]string{hook.URL + "/hooks/"}, filepath.Join(dir, "wakes"), hook.Client())} {
			if err != nil {
				return err
			}
		}
		return nil
	})
	connect := func(lines string) (net.Conn, *bufio.Reader) {
		conn, reader := rawConn(t, address)
		_, _ = io.WriteString(conn, lines)
		expectLine(t, reader, conn, " 001 ")
		return conn, reader
	}

	// Bad options are refused; good ones are stored and reported.
	carol, cr := connect("REGISTER carol :" + testAdminToken + "\r\nNICK carol\r\nUSER u 0 * :u\r\n")
	for _, bad := range []string{"mentions-only=1", "quiet-hours=22:00", "quiet-hours=22:00-22:00", "quiet-hours=25:00-08:00", "later", "mentions-only,mentions-only"} {
		_, _ = io.WriteString(carol, "WAKE SET "+hook.URL+"/hooks/carol "+secret+" 10 "+bad+"\r\n")
		expectLine(t, cr, carol, " 417 ")
	}
	_, _ = io.WriteString(carol, "WAKE SET "+hook.URL+"/hooks/carol "+secret+" 10 mentions-only,quiet-hours=22:00-08:00\r\n")
	line := expectLine(t, cr, carol, " 787 ")
	var status wakeStatus
	if err := json.Unmarshal([]byte(line[strings.Index(line, "{"):]), &status); err != nil || !status.Registered || !status.MentionsOnly || status.QuietHours != "22:00-08:00" || status.QuietUntil != nil {
		t.Fatalf("status %q: %v", line, err)
	}
	saved := map[string]savedWake{}
	data, err := os.ReadFile(filepath.Join(dir, "wakes"))
	if err != nil || json.Unmarshal(data, &saved) != nil || len(saved) != 1 {
		t.Fatalf("wakes file %v: %s", err, data)
	}
	for _, entry := range saved {
		if !entry.MentionsOnly || entry.QuietHours != "22:00-08:00" {
			t.Fatalf("saved registration %+v", entry)
		}
	}
	// A record from before the options loads with them unset.
	if mentionsOnly, quietHours, err := parseWakeOptions(""); err != nil || mentionsOnly || quietHours != "" {
		t.Fatal(mentionsOnly, quietHours, err)
	}

	// carol offers a handoff to bob in #room and leaves; bob's accept step
	// names her only in for, so mentions-only does not count it, while a
	// tag and a direct message do. Nothing goes during the quiet hours.
	offer, _ := json.Marshal(protocol.ChatRequest{Action: "handoff", Target: "#room", Text: "task 7", For: "bob"})
	_, _ = io.WriteString(carol, "JOIN #room\r\n")
	expectLine(t, cr, carol, " 366 ")
	_, _ = io.WriteString(carol, "CHAT :"+string(offer)+"\r\n")
	offerLine := expectLine(t, cr, carol, " 777 ")
	var entry protocol.ChatEntry
	if err := json.Unmarshal([]byte(offerLine[strings.Index(offerLine, "{"):]), &entry); err != nil || entry.ID == "" {
		t.Fatalf("offer %q: %v", offerLine, err)
	}
	_, _ = io.WriteString(carol, "QUIT\r\n")
	readUntilClosed(t, carol, cr)
	bob, br := connect("NICK bob\r\nUSER u 0 * :u\r\nJOIN #room\r\n")
	accept, _ := json.Marshal(protocol.ChatRequest{Action: "handoff-accept", ID: entry.ID})
	_, _ = io.WriteString(bob, "CHAT :"+string(accept)+"\r\n")
	expectLine(t, br, bob, " 778 ")
	pending := func() int {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		for _, state := range srv.wake.byAccount {
			return state.pending
		}
		return -1
	}
	if pending() != 0 {
		t.Fatalf("a step addressed by for alone counted: pending %d", pending())
	}
	_, _ = io.WriteString(bob, "PRIVMSG #room :carol: a tag\r\nPRIVMSG carol :a direct message\r\n")
	testwait.Until(t, 5*time.Second, "the tag and the direct message counted", func() bool { return pending() == 2 })
	receiver.none(t, 300*time.Millisecond)
	reader, rr := connect("AUTH carol :" + testAdminToken + "\r\nEPHEMERAL\r\nNICK carol\r\nUSER u 0 * :u\r\n")
	_, _ = io.WriteString(reader, "WAKE\r\n")
	line = expectLine(t, rr, reader, " 787 ")
	status = wakeStatus{}
	if err := json.Unmarshal([]byte(line[strings.Index(line, "{"):]), &status); err != nil || status.Pending != 2 || status.QuietUntil == nil || status.QuietUntil.Hour() != 8 || status.QuietUntil.Day() != 27 {
		t.Fatalf("status during quiet hours %q: %v", line, err)
	}
	_, _ = io.WriteString(reader, "QUIT\r\n")
	readUntilClosed(t, reader, rr)

	// The window ends: the next look at the registrations sends the news
	// as one notice.
	mu.Lock()
	now = time.Date(2026, 10, 27, 8, 0, 0, 0, time.Local)
	mu.Unlock()
	_, _ = io.WriteString(bob, "PRIVMSG carol :good morning\r\n")
	notice := receiver.next(t, 5*time.Second)
	if notice.Messages != 3 || notice.Nick != "carol" {
		t.Fatalf("notice after the quiet hours %+v", notice)
	}
}
