package server

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testwait"
)

// wakeReceiver collects signed notices, checking each signature.
type wakeReceiver struct {
	t       *testing.T
	secret  []byte
	notices chan wakeNotice
	refused atomic.Int32
}

func (r *wakeReceiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	mac := hmac.New(sha256.New, r.secret)
	mac.Write([]byte(req.Header.Get("AIRC-Timestamp") + "."))
	mac.Write(body)
	if req.Method != http.MethodPost || req.Header.Get("AIRC-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
		r.t.Errorf("unsigned or badly signed notice: %s %v", req.Method, req.Header)
	}
	if strings.HasSuffix(req.URL.Path, "/down") {
		r.refused.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	var notice wakeNotice
	if err := json.Unmarshal(body, &notice); err != nil {
		r.t.Error(err)
	}
	r.notices <- notice
}

func (r *wakeReceiver) next(t *testing.T, within time.Duration) wakeNotice {
	t.Helper()
	select {
	case n := <-r.notices:
		return n
	case <-time.After(within):
		t.Fatal("no wake notice")
		return wakeNotice{}
	}
}

func (r *wakeReceiver) none(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case n := <-r.notices:
		t.Fatalf("unexpected wake notice %+v", n)
	case <-time.After(within):
	}
}

func TestWakeNotices(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff"
	key, _ := hex.DecodeString(secret)
	receiver := &wakeReceiver{t: t, secret: key, notices: make(chan wakeNotice, 16)}
	hook := httptest.NewServer(receiver)
	defer hook.Close()
	dir := t.TempDir()
	start := func() string {
		address, _ := startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
			if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
				return err
			}
			return s.EnableWake([]string{hook.URL + "/hooks/"}, filepath.Join(dir, "wakes"), hook.Client())
		})
		return address
	}
	address := start()
	connect := func(lines string) (net.Conn, *bufio.Reader) {
		conn, reader := rawConn(t, address)
		_, _ = io.WriteString(conn, lines)
		expectLine(t, reader, conn, " 001 ")
		return conn, reader
	}

	// An unauthenticated connection cannot register; carol's account can,
	// for an allowlisted URL only.
	guest, guestReader := connect("NICK passer\r\nUSER u 0 * :u\r\n")
	_, _ = io.WriteString(guest, "WAKE SET "+hook.URL+"/hooks/carol "+secret+"\r\n")
	expectLine(t, guestReader, guest, " 481 ")
	carol, carolReader := connect("REGISTER carol :" + testAdminToken + "\r\nNICK carol\r\nUSER u 0 * :u\r\n")
	for _, outside := range []string{"https://elsewhere.example/", hook.URL + "/hooks/../admin", hook.URL + "/hooks/%2e%2e/admin", hook.URL + "/hooksx", hook.URL + "/hooks/..;/admin", hook.URL + "/hooks/x;..;/y", hook.URL + "/hooks//admin", hook.URL + "/hooks/x?email=a", hook.URL + "/hooks/.env"} {
		_, _ = io.WriteString(carol, "WAKE SET "+outside+" "+secret+"\r\n")
		expectLine(t, carolReader, carol, " 417 ")
	}
	_, _ = io.WriteString(carol, "WAKE SET "+hook.URL+"/hooks/carol "+secret+" 10\r\n")
	if line := expectLine(t, carolReader, carol, " 787 "); !strings.Contains(line, `"registered":true`) || strings.Contains(line, secret) {
		t.Fatalf("status %q", line)
	}

	frank, frankReader := connect("REGISTER frank :" + testAdminToken + "\r\nNICK frank\r\nUSER u 0 * :u\r\nWAKE SET " + hook.URL + "/hooks/down " + secret + "\r\nQUIT\r\n")
	readUntilClosed(t, frank, frankReader)
	bob, _ := connect("NICK bob\r\nUSER u 0 * :u\r\nJOIN #room\r\n")
	// carol is connected: her inbox wakes nobody.
	_, _ = io.WriteString(bob, "PRIVMSG carol :while you are here\r\n")
	receiver.none(t, 300*time.Millisecond)
	_, _ = io.WriteString(carol, "QUIT\r\n")
	readUntilClosed(t, carol, carolReader)

	// A direct message to the offline account wakes it at once; what
	// follows within the quiet period is gathered into the next notice.
	_, _ = io.WriteString(bob, "PRIVMSG carol :are you there?\r\n")
	first := receiver.next(t, 3*time.Second)
	if first.Event != "inbox" || first.Nick != "carol" || first.Messages != 1 || first.Latest == "" || first.Account == "" {
		t.Fatalf("notice %+v", first)
	}
	// A mention and a direct message count; a post that does not name her
	// and a reaction do not.
	_, _ = io.WriteString(bob, "PRIVMSG #room :carol: a mention\r\nPRIVMSG #room :no mention here\r\nPRIVMSG carol :and again\r\nREACT "+first.Latest+" :+1\r\n")
	// frank's hook refuses: the failure is kept for WAKE STATUS.
	_, _ = io.WriteString(bob, "PRIVMSG frank :your hook is down\r\n")
	testwait.Until(t, 5*time.Second, "frank's refused notice", func() bool { return receiver.refused.Load() == 1 })
	frank, frankReader = connect("AUTH frank :" + testAdminToken + "\r\nNICK frank\r\nUSER u 0 * :u\r\nWAKE\r\n")
	if line := expectLine(t, frankReader, frank, " 787 "); !strings.Contains(line, "HTTP 503") || !strings.Contains(line, `"pending":1`) {
		t.Fatalf("status after a refused notice %q", line)
	}
	receiver.none(t, 2*time.Second)
	second := receiver.next(t, 12*time.Second)
	if second.Messages != 2 || second.Latest == first.Latest {
		t.Fatalf("coalesced notice %+v", second)
	}
}

// Registrations outlive a restart; WAKE CLEAR removes them; without
// EnableWake the command is refused.
func TestWakeRegistrationsPersist(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff"
	dir := t.TempDir()
	enable := func(s *Server) error {
		if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
			return err
		}
		return s.EnableWake([]string{"https://hooks.example/"}, filepath.Join(dir, "wakes"), nil)
	}
	address, srv := startServer(t, Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}, enable)
	conn, reader := rawConn(t, address)
	_, _ = io.WriteString(conn, "REGISTER dave :"+testAdminToken+"\r\nNICK dave\r\nUSER u 0 * :u\r\nWAKE SET https://hooks.example/dave "+secret+"\r\n")
	expectLine(t, reader, conn, `"registered":true`)
	_ = conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	address, _ = startServer(t, Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}, enable)
	conn, reader = rawConn(t, address)
	_, _ = io.WriteString(conn, "AUTH dave :"+testAdminToken+"\r\nNICK dave\r\nUSER u 0 * :u\r\nWAKE\r\n")
	if line := expectLine(t, reader, conn, " 787 "); !strings.Contains(line, `"url":"https://hooks.example/dave"`) {
		t.Fatalf("restored status %q", line)
	}
	_, _ = io.WriteString(conn, "WAKE CLEAR\r\n")
	expectLine(t, reader, conn, `"registered":false`)

	plain := rawServer(t)
	conn, reader = rawConn(t, plain)
	_, _ = io.WriteString(conn, "NICK erin\r\nUSER u 0 * :u\r\nWAKE\r\n")
	expectLine(t, reader, conn, " 421 ")
}

// What an account registered never stops the daemon starting: a deleted
// account loses its registration and cannot make a new one, and saved
// registrations the daemon no longer accepts are dropped on start.
func TestWakeRegistrationsDropWithTheirAccount(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff"
	dir := t.TempDir()
	enable := func(allow string) func(*Server) error {
		return func(s *Server) error {
			for _, err := range []error{s.EnableAdmin(testAdminToken), s.RestoreAccounts(filepath.Join(dir, "accounts"))} {
				if err != nil {
					return err
				}
			}
			return s.EnableWake([]string{allow}, filepath.Join(dir, "wakes"), nil)
		}
	}
	config := Config{HistoryLimit: 8, PingInterval: time.Hour, ReadTimeout: time.Hour}
	address, srv := startServer(t, config, enable("https://hooks.example/"))
	register := func(nick string) (net.Conn, *bufio.Reader) {
		conn, reader := rawConn(t, address)
		_, _ = io.WriteString(conn, "REGISTER "+nick+" :"+testAdminToken+"\r\nNICK "+nick+"\r\nUSER u 0 * :u\r\nWAKE SET https://hooks.example/"+nick+" "+secret+"\r\n")
		expectLine(t, reader, conn, `"registered":true`)
		return conn, reader
	}
	dave, daveReader := register("dave")
	register("eve")
	control, controlReader := rawConn(t, address)
	_, _ = io.WriteString(control, "NICK op\r\nUSER u 0 * :u\r\nOPER :"+testAdminToken+"\r\n")
	expectLine(t, controlReader, control, " 381 ")
	if result := adminWire(t, control, controlReader, protocol.AdminRequest{Action: "account-delete", Nick: "dave"}); !result.Changed {
		t.Fatalf("delete: %+v", result)
	}
	// dave's connection outlives his account but cannot register again.
	_, _ = io.WriteString(dave, "WAKE SET https://hooks.example/dave "+secret+"\r\n")
	expectLine(t, daveReader, dave, " 481 ")
	srv.mu.Lock()
	registered := len(srv.wake.byAccount)
	srv.mu.Unlock()
	if registered != 1 {
		t.Fatalf("%d registrations after deleting dave, want eve's alone", registered)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	// The operator narrows the allowlist: eve's registration is dropped,
	// not fatal.
	address, _ = startServer(t, config, enable("https://other.example/"))
	conn, reader := rawConn(t, address)
	_, _ = io.WriteString(conn, "AUTH eve :"+testAdminToken+"\r\nNICK eve\r\nUSER u 0 * :u\r\nWAKE\r\n")
	expectLine(t, reader, conn, `"registered":false`)
}

// A failed notice's status names the kind of failure, not the addresses,
// resolvers or certificates in the error.
func TestWakeFailureIsCoarse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := listener.Addr().String()
	_ = listener.Close()
	direct := &http.Client{Transport: &http.Transport{}} // no proxy from the environment
	_, refused := direct.Post("http://"+closed+"/hooks/x", "application/json", nil)
	_, missing := direct.Post("http://airc-wake-test.invalid/hooks/x", "application/json", nil)
	for _, c := range []struct {
		err  error
		want string
	}{
		{refused, "notice not delivered: connection refused"},
		{missing, "notice not delivered: name lookup failed"},
		{wakeRefused(503), "notice refused: HTTP 503"},
		{context.DeadlineExceeded, "notice not delivered: timed out"},
	} {
		if got := wakeFailure(c.err); got != c.want {
			t.Errorf("wakeFailure(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

// News that has not been announced when aircd stops (here, held by the
// quiet period) is announced after it starts again, no earlier than the
// quiet period allows.
func TestWakeNewsOutlivesARestart(t *testing.T) {
	const secret = "00112233445566778899aabbccddeeff"
	key, _ := hex.DecodeString(secret)
	receiver := &wakeReceiver{t: t, secret: key, notices: make(chan wakeNotice, 16)}
	hook := httptest.NewServer(receiver)
	defer hook.Close()
	dir := t.TempDir()
	start := func() (string, *Server) {
		return startServer(t, Config{HistoryLimit: 64, PingInterval: time.Hour, ReadTimeout: time.Hour}, func(s *Server) error {
			if err := s.RestoreAccounts(filepath.Join(dir, "accounts")); err != nil {
				return err
			}
			return s.EnableWake([]string{hook.URL + "/hooks/"}, filepath.Join(dir, "wakes"), hook.Client())
		})
	}
	address, srv := start()
	carol, carolReader := rawConn(t, address)
	_, _ = io.WriteString(carol, "REGISTER carol :"+testAdminToken+"\r\nNICK carol\r\nUSER u 0 * :u\r\nWAKE SET "+hook.URL+"/hooks/carol "+secret+" 10\r\nQUIT\r\n")
	readUntilClosed(t, carol, carolReader)
	bob, bobReader := rawConn(t, address)
	_, _ = io.WriteString(bob, "NICK bob\r\nUSER u 0 * :u\r\nPRIVMSG carol :first\r\n")
	expectLine(t, bobReader, bob, " 001 ")
	first := receiver.next(t, 3*time.Second)
	sentAt := time.Now()
	_, _ = io.WriteString(bob, "PRIVMSG carol :held by the quiet period\r\n")
	testwait.Until(t, 5*time.Second, "the held message counted", func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		for _, state := range srv.wake.byAccount {
			if state.pending == 1 && state.latest != first.Latest && !state.sending {
				return true
			}
		}
		return false
	})
	_ = bob.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}

	start()
	second := receiver.next(t, 15*time.Second)
	if second.Messages != 1 || second.Latest == first.Latest || second.Latest == "" {
		t.Fatalf("notice after the restart %+v", second)
	}
	if early := time.Since(sentAt); early < 9*time.Second {
		t.Fatalf("the restart cut the quiet period short: %v after the first notice", early)
	}
}
