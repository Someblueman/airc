package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/testtls"
)

func authenticatedAgent(t *testing.T, rtt time.Duration) (*mcpAdapter, *agentProxy, options) {
	t.Helper()
	cert := testtls.New(t)
	address := cliTestServerSetup(t, server.Config{TLSConfig: cert.Server, HistoryLimit: 64}, func(s *server.Server) error { return s.RestoreAccounts(filepath.Join(t.TempDir(), "accounts")) })
	proxy := delayedAgentProxy(t, address, rtt)
	flags := []string{"--addr", proxy.address, "--tls-ca", cert.CAPath, "--nick", "warm-agent", "--json"}
	if err := runUser(append([]string{"create"}, flags...), io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a := &mcpAdapter{flags: flags, slots: make(chan struct{}, 4), waits: make(chan struct{}, 2), lifetime: ctx}
	t.Cleanup(func() { cancel(); a.closeConnections() })
	return a, proxy, options{addr: proxy.address, tlsCA: cert.CAPath, nick: "warm-agent"}
}

func TestMCPReusesTLSAccountAndRecoversAfterCredentialRejection(t *testing.T) {
	agentEnv(t)
	a, proxy, opt := authenticatedAgent(t, 0)
	before := proxy.accepted.Load()
	agentCallOK(t, a, []string{"directory", "--who", "warm-agent"})
	first := proxy.accepted.Load()
	agentCallOK(t, a, []string{"directory", "--who", "warm-agent"})
	if first != before+1 || proxy.accepted.Load() != first {
		t.Fatal("second warm call logged in again")
	}
	closeIdleAgentClients(a)
	path, err := identityPath(opt)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var identity savedIdentity
	if err := json.Unmarshal(saved, &identity); err != nil {
		t.Fatal(err)
	}
	identity.Token = strings.Repeat("0", 64)
	bad, _ := json.Marshal(identity)
	if err := os.WriteFile(path, bad, 0600); err != nil {
		t.Fatal(err)
	}
	r, _, err := a.call(context.Background(), []string{"directory"}, "", time.Second, false)
	if err == nil && (r == nil || !r.IsError) {
		t.Fatal("bad credentials accepted")
	}
	if err := os.WriteFile(path, saved, 0600); err != nil {
		t.Fatal(err)
	}
	agentCallOK(t, a, []string{"directory"})
	// Cancel an observed wait; later reads must neither reuse its subscriptions
	// nor consume a leftover response from the interrupted stream.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.call(ctx, []string{"check", "--mentions", "--wait", "10s"}, "", 11*time.Second, true)
	}()
	deadline := time.Now().Add(time.Second)
	for len(a.waits) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("wait cancellation leaked")
	}
	if len(a.slots) != 0 || len(a.waits) != 0 {
		t.Fatal("wait leaked capacity")
	}
	agentCallOK(t, a, []string{"directory"})
}

func TestMCPColdWarmLatency(t *testing.T) {
	if os.Getenv("AIRC_WARM_MEASURE") != "1" {
		t.Skip("opt-in simulated RTT measurement")
	}
	agentEnv(t)
	for _, milliseconds := range []int{0, 25, 100} {
		t.Run(fmt.Sprint(milliseconds), func(t *testing.T) {
			a, _, _ := authenticatedAgent(t, time.Duration(milliseconds)*time.Millisecond)
			for i := 0; i < 3; i++ {
				closeIdleAgentClients(a)
				cold := agentCallOK(t, a, []string{"directory", "--who", "warm-agent"})
				warm := agentCallOK(t, a, []string{"directory", "--who", "warm-agent"})
				t.Logf("nominal_rtt_ms=%d sample=%d cold_ms=%.3f warm_ms=%.3f", milliseconds, i, float64(cold)/float64(time.Millisecond), float64(warm)/float64(time.Millisecond))
			}
		})
	}
}

func TestMCPWarmSendRecoversDroppedReceipt(t *testing.T) {
	address := chatServer(t, 64)
	proxy := dropFirstReceipt(t, address)
	ctx, cancel := context.WithCancel(context.Background())
	a := &mcpAdapter{flags: []string{"--addr", proxy, "--nick", "writer", "--json"}, slots: make(chan struct{}, 4), waits: make(chan struct{}, 2), lifetime: ctx}
	defer func() { cancel(); a.closeConnections() }()
	agentCallOK(t, a, []string{"directory"}) // warm the connection before its lost receipt
	r, output, err := a.call(ctx, []string{"send", "--channel", "#room", "--message", "only once", "--request-id", "warm-once"}, "", 5*time.Second, false)
	if err != nil || r != nil && r.IsError {
		t.Fatalf("receipt recovery failed: %v %+v", err, output)
	}
	agentCallOK(t, a, []string{"send", "--retry", "warm-once"})
	agentCallOK(t, a, []string{"directory"})
	if ids := historyIDs(t, mustCLI(t, address, "history", "#room", "--json")); len(ids) != 1 {
		t.Fatal("receipt recovery duplicated the message", ids)
	}
}
