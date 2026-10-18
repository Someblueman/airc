package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// Wake notices reach an account that holds no connection. The account
// registers a URL with WAKE SET; when a message enters its inbox (a direct
// message, or a mention outside a room it is banned from; never a reaction
// or its own post) while it has no live session, aircd POSTs a signed
// notice that names no sender and carries no text, at most once per quiet
// period. Only URLs under the operator's allowlist are accepted.
const (
	maxWakes         = 1024
	maxWakeURLBytes  = 512
	wakeTTL          = 30 * 24 * time.Hour
	wakeTimeout      = 5 * time.Second
	wakeMinQuiet     = 10 * time.Second
	wakeMaxQuiet     = time.Hour
	wakeDefaultQuiet = time.Minute
	wakeMaxBackoff   = time.Hour
	// wakeSenders bounds the notices in flight at once.
	wakeSenders = 16
)

// wakeRegistration is what the wakes file keeps for an account.
type wakeRegistration struct {
	Account      string    `json:"account_id"`
	Nick         string    `json:"nick"`
	URL          string    `json:"url"`
	Secret       string    `json:"secret"`
	QuietSeconds int       `json:"quiet_seconds"`
	ExpiresAt    time.Time `json:"expires_at"`
}

type wakeState struct {
	reg wakeRegistration
	// pending counts inbox messages since the last notice, latest the
	// newest of them.
	pending    int
	latest     string
	nextAt     time.Time // the next notice goes no earlier
	failures   int
	sending    bool
	lastNotice time.Time
	lastError  string
}

type wakes struct {
	allow     []string
	path      string
	byAccount map[string]*wakeState
	kick      chan struct{}
	stop      chan struct{}
	client    *http.Client
	// pruned is when expired registrations and those of deleted accounts
	// were last dropped: once a minute, not on every inbox message.
	pruned time.Time
}

// wakeNotice is the body aircd POSTs. It says whose inbox has news and how
// much, never what: the receiver reads it with its own credentials.
type wakeNotice struct {
	Version  int       `json:"version"`
	Event    string    `json:"event"`
	Nick     string    `json:"nick"`
	Account  string    `json:"account_id"`
	Messages int       `json:"messages"`
	Latest   string    `json:"latest"`
	At       time.Time `json:"at"`
}

// EnableWake turns on wake notices to URLs starting with one of allow,
// keeping registrations in path. client sends the notices (nil: a client
// that follows no redirects). Call it before Serve, after RestoreAccounts.
func (s *Server) EnableWake(allow []string, path string, client *http.Client) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.wake != nil {
		return errors.New("configure wake notices once before Serve")
	}
	if len(allow) == 0 || path == "" {
		return errors.New("wake notices need an allowlist and a file")
	}
	for _, prefix := range allow {
		if !protocol.ValidWakePrefix(prefix) {
			return errors.New("each wake allowlist entry must be an http or https URL whose path ends in /, such as https://ntfy.sh/")
		}
	}
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	w := &wakes{allow: allow, path: path, byAccount: map[string]*wakeState{}, kick: make(chan struct{}, 1), stop: make(chan struct{}), client: client}
	saved := map[string]wakeRegistration{}
	if err := readState(path, &saved); err != nil {
		return err
	}
	// A registration the daemon would no longer accept (its account was
	// deleted or changed, its URL left the allowlist, or it expired) is
	// dropped, not fatal: starting must not depend on what an account
	// registered.
	now := s.now()
	for id, reg := range saved {
		if id != reg.Account || !s.wakeValidLocked(w, reg) || !now.Before(reg.ExpiresAt) {
			s.logger.Warn("wake_registration_dropped", "account_id", reg.Account, "nick", reg.Nick)
			continue
		}
		w.byAccount[id] = &wakeState{reg: reg}
	}
	if len(w.byAccount) != len(saved) {
		if err := s.saveWakesLocked(w, w.byAccount); err != nil {
			return err
		}
	}
	s.wake = w
	s.wg.Add(1)
	go s.runWakes(w, w.stop)
	return nil
}

// wakeValidLocked checks a registration against the accounts and the
// allowlist.
func (s *Server) wakeValidLocked(w *wakes, reg wakeRegistration) bool {
	a, ok := s.accounts[reg.Nick]
	return ok && a.ID == reg.Account && w.allowed(reg.URL) && validWakeSecret(reg.Secret) && reg.QuietSeconds >= int(wakeMinQuiet/time.Second) && reg.QuietSeconds <= int(wakeMaxQuiet/time.Second)
}

// allowed reports a URL under an allowlist prefix. Its path must be plain
// (protocol.PlainWakePath) and it takes no query, since the receiving
// server may read dot segments, parameters or escapes as a path outside
// the prefix, or a query as options the operator never allowed.
func (w *wakes) allowed(target string) bool {
	if len(target) > maxWakeURLBytes {
		return false
	}
	u, err := url.Parse(target)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != target || !protocol.PlainWakePath(u.Path) {
		return false
	}
	for _, prefix := range w.allow {
		if strings.HasPrefix(target, prefix) {
			return true
		}
	}
	return false
}

// validWakeSecret accepts 128 to 512 bits in lowercase hex.
func validWakeSecret(secret string) bool {
	if len(secret) < 32 || len(secret) > 128 || len(secret)%2 != 0 || strings.ToLower(secret) != secret {
		return false
	}
	_, err := hex.DecodeString(secret)
	return err == nil
}

func (s *Server) saveWakesLocked(w *wakes, next map[string]*wakeState) error {
	saved := make(map[string]wakeRegistration, len(next))
	for id, state := range next {
		saved[id] = state.reg
	}
	return writeState(w.path, saved, s.softSync())
}

// wakeLocked serves WAKE [STATUS], WAKE SET url secret [quiet-seconds] and
// WAKE CLEAR for the connection's account.
func (s *Server) wakeLocked(client *session, command protocol.Command) {
	w := s.wake
	if w == nil {
		s.numericLocked(client, "437", nil, "Wake notices are not enabled on this daemon")
		return
	}
	if a, ok := s.accounts[client.authNick]; client.accountID == "" || !ok || a.ID != client.accountID {
		s.numericLocked(client, "481", nil, "WAKE needs an authenticated account")
		return
	}
	sub, _ := command.Param(0)
	switch strings.ToUpper(sub) {
	case "", "STATUS":
	case "CLEAR":
		if _, ok := w.byAccount[client.accountID]; ok {
			next := make(map[string]*wakeState, len(w.byAccount))
			for id, state := range w.byAccount {
				if id != client.accountID {
					next[id] = state
				}
			}
			if err := s.saveWakesLocked(w, next); err != nil {
				s.numericLocked(client, "437", nil, "Unable to save wake registrations; no change applied")
				return
			}
			w.byAccount = next
		}
	case "SET":
		target, _ := command.Param(1)
		secret, _ := command.Param(2)
		quiet := wakeDefaultQuiet
		if raw, ok := command.Param(3); ok {
			seconds, err := strconv.Atoi(raw)
			if err != nil || time.Duration(seconds)*time.Second < wakeMinQuiet || time.Duration(seconds)*time.Second > wakeMaxQuiet {
				s.numericLocked(client, "417", nil, "Quiet period must be 10-3600 seconds")
				return
			}
			quiet = time.Duration(seconds) * time.Second
		}
		if target == "" || secret == "" {
			s.numericLocked(client, "461", []string{"WAKE"}, "WAKE SET needs a URL and a secret")
			return
		}
		if !w.allowed(target) {
			s.numericLocked(client, "417", nil, "Wake URL is not on this daemon's allowlist")
			return
		}
		if !validWakeSecret(secret) {
			s.numericLocked(client, "417", nil, "Wake secret must be 32-128 lowercase hex characters")
			return
		}
		old := w.byAccount[client.accountID]
		if old == nil && len(w.byAccount) >= maxWakes {
			s.numericLocked(client, "437", nil, "Wake registration limit reached")
			return
		}
		state := &wakeState{reg: wakeRegistration{Account: client.accountID, Nick: client.authNick, URL: target, Secret: secret, QuietSeconds: int(quiet / time.Second), ExpiresAt: s.now().Add(wakeTTL)}}
		next := make(map[string]*wakeState, len(w.byAccount)+1)
		for id, existing := range w.byAccount {
			next[id] = existing
		}
		next[client.accountID] = state
		if err := s.saveWakesLocked(w, next); err != nil {
			s.numericLocked(client, "437", nil, "Unable to save wake registrations; no change applied")
			return
		}
		w.byAccount = next
	default:
		s.numericLocked(client, "461", []string{"WAKE"}, "WAKE takes STATUS, SET or CLEAR")
		return
	}
	s.wakeStatusLocked(client, w)
}

// wakeStatus is the 787 reply: the account's registration, never its
// secret, and how its notices have gone.
type wakeStatus struct {
	Registered   bool       `json:"registered"`
	URL          string     `json:"url,omitempty"`
	QuietSeconds int        `json:"quiet_seconds,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	Pending      int        `json:"pending,omitempty"`
	LastNotice   *time.Time `json:"last_notice,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
}

func (s *Server) wakeStatusLocked(client *session, w *wakes) {
	status := wakeStatus{}
	if state := w.byAccount[client.accountID]; state != nil {
		status = wakeStatus{Registered: true, URL: state.reg.URL, QuietSeconds: state.reg.QuietSeconds, ExpiresAt: &state.reg.ExpiresAt, Pending: state.pending, LastError: state.lastError}
		if !state.lastNotice.IsZero() {
			status.LastNotice = &state.lastNotice
		}
	}
	data, _ := json.Marshal(status)
	s.numericLocked(client, "787", nil, string(data))
}

// noteInboxLocked marks the registered accounts whose inbox message enters,
// with no live session, for a notice. It follows the inbox's own rules:
// mentions in a channel (not the sender, not where the account is banned)
// and direct messages; reactions wake nobody.
func (s *Server) noteInboxLocked(message Message, mentions []string) {
	w := s.wake
	if w == nil || len(w.byAccount) == 0 || message.Reaction != "" {
		return
	}
	var nicks []string
	if protocol.IsChannel(message.Target) {
		for _, nick := range mentions {
			if strings.EqualFold(nick, message.From) {
				continue
			}
			if _, banned := s.restrictionLocked("ban", nick, message.Target); banned {
				continue
			}
			nicks = append(nicks, nickKey(nick))
		}
	} else if !strings.EqualFold(message.Target, message.From) {
		nicks = append(nicks, nickKey(message.Target))
	}
	woke := false
	for _, key := range nicks {
		a, ok := s.accounts[key]
		if !ok {
			continue
		}
		state := w.byAccount[a.ID]
		if state == nil || state.reg.Nick != key || s.liveNickLocked(key) != nil {
			continue
		}
		state.pending++
		state.latest = message.ID
		woke = true
	}
	if woke {
		select {
		case w.kick <- struct{}{}:
		default:
		}
	}
}

// runWakes sends due notices until stop closes, when the server shuts
// down and cancels the notices in flight.
func (s *Server) runWakes(w *wakes, stop <-chan struct{}) {
	defer s.wg.Done()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		<-stop
		cancel()
	}()
	done := make(chan struct{}, wakeSenders)
	inFlight := 0
	defer func() {
		for ; inFlight > 0; inFlight-- {
			<-done
		}
	}()
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		s.mu.Lock()
		due, wait := s.dueWakesLocked(w, time.Now(), wakeSenders-inFlight)
		s.mu.Unlock()
		for _, d := range due {
			inFlight++
			go func() {
				err := w.send(ctx, d)
				s.mu.Lock()
				s.wakeSentLocked(w, d, err, time.Now())
				s.mu.Unlock()
				done <- struct{}{}
			}()
		}
		timer.Reset(wait)
		select {
		case <-stop:
			return
		case <-w.kick:
		case <-done:
			inFlight--
		case <-timer.C:
		}
	}
}

// dueWake is one notice to send: its registration as it stood and the
// messages it covers.
type dueWake struct {
	state  *wakeState
	reg    wakeRegistration
	notice wakeNotice
}

// dueWakesLocked drops expired registrations and those whose account is
// gone (at most once a minute), and returns up to room notices that may go now, with how long to
// wait before looking again.
func (s *Server) dueWakesLocked(w *wakes, now time.Time, room int) ([]dueWake, time.Duration) {
	wait := time.Minute
	if now.Sub(w.pruned) >= time.Minute {
		w.pruned = now
		expired := false
		for id, state := range w.byAccount {
			if !s.now().Before(state.reg.ExpiresAt) || !s.wakeValidLocked(w, state.reg) {
				delete(w.byAccount, id)
				expired = true
			}
		}
		if expired {
			if err := s.saveWakesLocked(w, w.byAccount); err != nil {
				s.logger.Error("wake_save_failed", "error", err.Error())
			}
		}
	}
	var due []dueWake
	for _, state := range w.byAccount {
		if state.pending == 0 || state.sending {
			continue
		}
		if until := state.nextAt.Sub(now); until > 0 {
			wait = min(wait, until)
			continue
		}
		if len(due) >= room {
			continue
		}
		state.sending = true
		due = append(due, dueWake{state: state, reg: state.reg, notice: wakeNotice{Version: 1, Event: "inbox", Nick: state.reg.Nick, Account: state.reg.Account, Messages: state.pending, Latest: state.latest, At: s.now().UTC()}})
	}
	return due, max(wait, 10*time.Millisecond)
}

// wakeSentLocked records a notice's outcome. Messages that arrived while
// it was in flight stay pending for the next one.
func (s *Server) wakeSentLocked(w *wakes, d dueWake, err error, now time.Time) {
	state := d.state
	state.sending = false
	if w.byAccount[d.reg.Account] != state {
		return // cleared or replaced meanwhile
	}
	if err != nil {
		state.failures++
		state.lastError = wakeFailure(err)
		state.nextAt = now.Add(min(30*time.Second<<min(state.failures-1, 7), wakeMaxBackoff))
		s.logger.Warn("wake_failed", "account_id", d.reg.Account, "error", err.Error())
		return
	}
	state.failures, state.lastError, state.lastNotice = 0, "", now
	state.pending = max(state.pending-d.notice.Messages, 0)
	state.nextAt = now.Add(time.Duration(d.reg.QuietSeconds) * time.Second)
}

// send POSTs the notice, signed with the registration's secret over the
// timestamp and body as AIRC-Signature: sha256=HEX(HMAC(secret,
// timestamp + "." + body)). Any 2xx answer is delivery.
func (w *wakes) send(ctx context.Context, d dueWake) error {
	body, err := json.Marshal(d.notice)
	if err != nil {
		return err
	}
	key, _ := hex.DecodeString(d.reg.Secret)
	timestamp := strconv.FormatInt(d.notice.At.Unix(), 10)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	ctx, cancel := context.WithTimeout(ctx, wakeTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.reg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "aircd-wake/1")
	request.Header.Set("AIRC-Timestamp", timestamp)
	request.Header.Set("AIRC-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response, err := w.client.Do(request)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	_ = response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return wakeRefused(response.StatusCode)
	}
	return nil
}

// wakeRefused is a receiver's non-2xx answer.
type wakeRefused int

func (code wakeRefused) Error() string { return "notice refused: HTTP " + strconv.Itoa(int(code)) }

// wakeFailure is what WAKE STATUS says of a failed notice: the receiver's
// HTTP status or the kind of network failure, never the error text, which
// names addresses, resolvers, proxies and certificates of hosts the
// account cannot otherwise see. The log keeps the full error.
func wakeFailure(err error) string {
	var refused wakeRefused
	var dns *net.DNSError
	var certificate *tls.CertificateVerificationError
	var record tls.RecordHeaderError
	switch {
	case errors.As(err, &refused):
		return refused.Error()
	case errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded):
		return "notice not delivered: timed out"
	case errors.As(err, &dns):
		return "notice not delivered: name lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "notice not delivered: connection refused"
	case errors.As(err, &certificate) || errors.As(err, &record):
		return "notice not delivered: TLS failed"
	default:
		return "notice not delivered"
	}
}
