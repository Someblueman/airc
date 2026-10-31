//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/backend"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/internal/testwait"
	"github.com/Someblueman/airc/pkg/irc"
)

// This is deliberately opt-in: it exercises an installed external proxy rather
// than making mitmproxy part of the ordinary integration-test prerequisites.
func TestHTTPConnectProxyPassThroughTLSAcceptance(t *testing.T) {
	t.Parallel()
	if os.Getenv("AGW_CONNECT_PROXY") != "1" {
		t.Skip("requires AGW_CONNECT_PROXY=1 and installed mitmdump")
	}
	mitmdump, err := exec.LookPath("mitmdump")
	if err != nil {
		t.Fatal("AGW_CONNECT_PROXY=1 requires installed mitmdump")
	}
	versionCtx, versionCancel := context.WithTimeout(t.Context(), 5*time.Second)
	version, err := exec.CommandContext(versionCtx, mitmdump, "--version").CombinedOutput()
	versionCancel()
	if err != nil {
		t.Fatalf("inspect mitmdump version: %v: %s", err, version)
	}
	t.Logf("HTTP CONNECT proxy: %s", strings.TrimSpace(string(version)))

	f := gatewayHTTPFixture(t, false)
	origin, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := startPassThroughMITM(t, mitmdump, origin.Host)

	// Trust only this disposable HTTPS server certificate. The client still
	// performs ordinary x509 chain and hostname verification through CONNECT.
	roots := x509.NewCertPool()
	roots.AddCert(f.server.Certificate())
	transport := &http.Transport{
		Proxy: http.ProxyURL(proxy.address),
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    roots,
		},
	}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	t.Cleanup(func() { transport.CloseIdleConnections() })
	call := func(method, path, token string, body []byte) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, f.server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			setAuthorHeaders(req, token)
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("HTTP CONNECT request %s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		if resp.TLS == nil || resp.TLS.Version < tls.VersionTLS13 || len(resp.TLS.VerifiedChains) == 0 || len(resp.TLS.PeerCertificates) == 0 || !resp.TLS.PeerCertificates[0].Equal(f.server.Certificate()) {
			t.Fatalf("origin TLS was not normally verified through CONNECT: %+v", resp.TLS)
		}
		const maxResponse = 1 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > maxResponse {
			t.Fatal("CONNECT response exceeded bounded read")
		}
		return resp.StatusCode, data
	}

	room := &core.Target{Kind: "room", Name: "#gateway-fixture"}
	requests := []core.Request{
		{Version: 1, RequestID: "connect-send-one", Operation: core.Send, Target: room, Message: "CONNECT body one\nwhole body 🙂"},
		{Version: 1, RequestID: "connect-send-two", Operation: core.Send, Target: room, Message: "CONNECT body two\nsecond whole body"},
	}
	if status, _ := call(http.MethodPost, "/v1/messages", "bad nick", requestJSON(t, requests[0])); status != http.StatusBadRequest {
		t.Fatalf("invalid author status=%d want=%d", status, http.StatusBadRequest)
	}
	if status, _ := call(http.MethodPost, "/v1/messages", f.token, bytes.Repeat([]byte(" "), core.MaxRequestBytes+1)); status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized CONNECT input status=%d", status)
	}
	if messages := nativeWebTransportMessages(t, f.socket, room.Name); len(messages) != 0 {
		t.Fatalf("invalid author reached native dispatch: %+v", messages)
	}

	var observed []*irc.MessageEvent
	for _, request := range requests {
		body := requestJSON(t, request)
		status, data := call(http.MethodPost, "/v1/messages", f.token, body)
		var admitted core.Result
		if err := json.Unmarshal(data, &admitted); err != nil || status != http.StatusAccepted || !admitted.Durable || admitted.RequestID != request.RequestID {
			t.Fatalf("CONNECT send admission status=%d result=%+v err=%v", status, admitted, err)
		}
		job := awaitDelivery(t, f.journal, f.identity, request.RequestID, func(j store.Job) bool { return j.Result.State == core.Accepted })
		seen := next(t, f.observer, func(e *irc.MessageEvent) bool { return e.ID == job.Result.MessageID })
		if seen.From != "fixture-author" || seen.Target != room.Name || seen.Message != request.Message {
			t.Fatalf("CONNECT native observer mismatch: %+v", seen)
		}
		observed = append(observed, seen)

		// Replay the exact same durable request through the verified tunnel.
		status, data = call(http.MethodPost, "/v1/messages", f.token, body)
		if err := json.Unmarshal(data, &admitted); err != nil || status != http.StatusAccepted || admitted.MessageID != seen.ID || admitted.SendAttempts != 1 || admitted.State != core.Accepted {
			t.Fatalf("CONNECT exact replay changed acceptance: status=%d result=%+v err=%v", status, admitted, err)
		}
	}

	query := url.Values{"kind": {"room"}, "target": {room.Name}, "limit": {"1"}, "start": {"retained"}}
	var paged []*irc.MessageMetadata
	var cursor string
	for i, want := range observed {
		status, data := call(http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil)
		var page backend.HistoryPage
		if err := json.Unmarshal(data, &page); err != nil || status != http.StatusOK {
			t.Fatalf("CONNECT history page %d status=%d body=%s err=%v", i+1, status, data, err)
		}
		wantStatus := "more"
		if i == len(observed)-1 {
			wantStatus = "ok"
		}
		if len(page.Messages) != 1 || page.Messages[0].ID != want.ID || page.Messages[0].From != want.From || page.Messages[0].Target != want.Target || page.Messages[0].Message != want.Message || page.Status != wantStatus || page.Cursor == "" || page.Gap || page.Cursor == cursor {
			t.Fatalf("CONNECT page %d differs from native observer: %+v want=%+v", i+1, page, want)
		}
		paged = append(paged, page.Messages[0])
		cursor = page.Cursor
		query.Del("start")
		query.Set("cursor", cursor)
	}
	check := nativeWebTransportMessages(t, f.socket, room.Name)
	if len(check) != len(paged) || len(check) != len(observed) {
		t.Fatalf("CONNECT history/CHECK counts: pages=%d CHECK=%d observer=%d", len(paged), len(check), len(observed))
	}
	for i := range check {
		if check[i].ID != paged[i].ID || check[i].From != paged[i].From || check[i].Target != paged[i].Target || check[i].Message != paged[i].Message || check[i].ID != observed[i].ID || check[i].Message != observed[i].Message {
			t.Fatalf("CONNECT ordered history differs from native CHECK at %d: page=%+v CHECK=%+v observer=%+v", i, paged[i], check[i], observed[i])
		}
	}
	status, data := call(http.MethodGet, "/v1/history?"+query.Encode(), f.token, nil)
	var exhausted backend.HistoryPage
	if err := json.Unmarshal(data, &exhausted); err != nil || status != http.StatusOK || len(exhausted.Messages) != 0 || exhausted.Status != "ok" || exhausted.Cursor == "" || exhausted.Gap {
		t.Fatalf("CONNECT exhausted cursor response status=%d page=%+v err=%v", status, exhausted, err)
	}
	proxy.assertConnectObserved(t, origin.Host)
}

type passThroughProxy struct {
	address    *url.URL
	cmd        *exec.Cmd
	done       chan struct{}
	err        error
	stderrPath string
	logPath    string
}

func startPassThroughMITM(t *testing.T, mitmdump, originHost string) *passThroughProxy {
	t.Helper()
	root := t.TempDir()
	logPath := filepath.Join(root, "connect.log")
	stderrPath := filepath.Join(root, "mitmdump.stderr")
	scriptPath := filepath.Join(root, "connect-log.py")
	script := `from mitmproxy import http
import os

class ConnectLog:
    def http_connect(self, flow: http.HTTPFlow):
        with open(os.environ["AGW_CONNECT_LOG"], "a", encoding="utf-8") as out:
            out.write(f"{flow.request.host}:{flow.request.port}\n")

addons = [ConnectLog()]
`
	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	confdir := filepath.Join(root, "mitmproxy")
	if err := os.Mkdir(confdir, 0700); err != nil {
		t.Fatal(err)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := reservation.Addr().(*net.TCPAddr).Port
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	proxyHost := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if _, _, err := net.SplitHostPort(originHost); err != nil {
		t.Fatalf("fixture origin is not host:port: %q: %v", originHost, err)
	}
	// mitmproxy 12.2.3 next_layer matches the destination as host:port.
	// Exclude only this fixture authority from certificate interception.
	ignore := "^" + regexp.QuoteMeta(originHost) + "$"
	cmd := exec.Command(mitmdump,
		"--mode", "regular",
		"--listen-host", "127.0.0.1",
		"--listen-port", strconv.Itoa(port),
		"--ignore-hosts", ignore,
		"--set", "confdir="+confdir,
		"-s", scriptPath,
	)
	cmd.Env = append(os.Environ(), "AGW_CONNECT_LOG="+logPath)
	proxy := &passThroughProxy{cmd: cmd, done: make(chan struct{}), logPath: logPath, stderrPath: stderrPath}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = stderrFile
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		_ = stderrFile.Close()
		t.Fatalf("start mitmdump: %v", err)
	}
	if err := stderrFile.Close(); err != nil {
		t.Fatalf("close parent mitmdump stderr handle: %v", err)
	}
	go func() {
		proxy.err = cmd.Wait()
		close(proxy.done)
	}()
	t.Cleanup(func() {
		proxy.stop(t)
	})
	if !testwait.Poll(5*time.Second, 25*time.Millisecond, func() bool {
		select {
		case <-proxy.done:
			t.Fatalf("mitmdump exited during startup: %v; stderr=%s", proxy.err, proxy.stderr())
		default:
		}
		conn, dialErr := net.DialTimeout("tcp", proxyHost, 100*time.Millisecond)
		if dialErr == nil {
			_ = conn.Close()
		}
		return dialErr == nil
	}) {
		t.Fatalf("mitmdump did not listen; stderr is in %s", proxy.stderrPath)
	}
	proxy.address = &url.URL{Scheme: "http", Host: proxyHost}
	return proxy
}

func (p *passThroughProxy) stop(t *testing.T) {
	t.Helper()
	select {
	case <-p.done:
		t.Errorf("mitmdump exited before owned interrupt shutdown: %v; stderr=%s", p.err, p.stderr())
		return
	default:
	}
	if err := p.cmd.Process.Signal(os.Interrupt); err != nil {
		t.Errorf("interrupt owned mitmdump: %v", err)
	}
	select {
	case <-p.done:
		if p.err != nil {
			t.Errorf("mitmdump interrupt shutdown failed: %v; stderr=%s", p.err, p.stderr())
		}
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
			t.Errorf("mitmdump did not stop successfully on interrupt: %v; stderr=%s", p.err, p.stderr())
		case <-time.After(2 * time.Second):
			t.Errorf("mitmdump did not join after kill; stderr path=%s", p.stderrPath)
		}
	}
}

func (p *passThroughProxy) stderr() string {
	data, err := os.ReadFile(p.stderrPath)
	if err != nil {
		return err.Error()
	}
	return string(data)
}

func (p *passThroughProxy) assertConnectObserved(t *testing.T, originHost string) {
	t.Helper()
	var lines []string
	if !testwait.Poll(2*time.Second, 25*time.Millisecond, func() bool {
		data, err := os.ReadFile(p.logPath)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read mitmdump CONNECT evidence: %v", err)
		}
		lines = strings.Fields(string(data))
		return len(lines) > 0
	}) {
		t.Fatalf("mitmdump observed no CONNECT request for %s; stderr=%s", originHost, p.stderr())
	}
	for _, line := range lines {
		if line != originHost {
			t.Fatalf("mitmdump tunneled unexpected CONNECT authority %q; want only %q", line, originHost)
		}
	}
}
