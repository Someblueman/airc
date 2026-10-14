//go:build darwin || linux

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	gwconfig "github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/internal/service"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/Someblueman/airc/pkg/fallback"
)

// udpRelay forwards datagrams between one client and address. rebind moves
// its upstream side to a new socket, so the server sees the client arrive
// from a new address and port, as after a NAT rebinding or a move from
// Wi-Fi to cellular.
type udpRelay struct {
	listen *net.UDPConn
	target *net.UDPAddr
	mu     sync.Mutex
	up     *net.UDPConn
	client *net.UDPAddr
}

func startUDPRelay(t *testing.T, address string) *udpRelay {
	t.Helper()
	target, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		t.Fatal(err)
	}
	listen, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r := &udpRelay{listen: listen, target: target}
	r.rebind(t)
	t.Cleanup(func() {
		_ = listen.Close()
		r.mu.Lock()
		_ = r.up.Close()
		r.mu.Unlock()
	})
	go func() {
		buf := make([]byte, 65536)
		for {
			n, from, err := listen.ReadFromUDP(buf)
			if err != nil {
				return
			}
			r.mu.Lock()
			r.client = from
			up := r.up
			r.mu.Unlock()
			_, _ = up.WriteToUDP(buf[:n], target)
		}
	}()
	return r
}

func (r *udpRelay) addr() *net.UDPAddr { return r.listen.LocalAddr().(*net.UDPAddr) }

func (r *udpRelay) rebind(t *testing.T) {
	t.Helper()
	up, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	old := r.up
	r.up = up
	r.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	go func() {
		buf := make([]byte, 65536)
		for {
			n, err := up.Read(buf)
			if err != nil {
				return
			}
			r.mu.Lock()
			client := r.client
			r.mu.Unlock()
			if client != nil {
				_, _, _ = r.listen.WriteMsgUDP(buf[:n], nil, client)
			}
		}
	}()
}

// http3Fixture is a daemon whose gateway serves HTTP/3 on a loopback UDP
// port with a test certificate.
func http3Fixture(t *testing.T) (daemon string, port int, cert testtls.Certificate) {
	t.Helper()
	agentEnv(t)
	cert = testtls.New(t)
	daemon = cliTestServerSetup(t, server.Config{HistoryLimit: 64}, func(s *server.Server) error { return s.RestoreAccounts(filepath.Join(t.TempDir(), "accounts")) })
	port = freeUDPPort(t)
	listener := gwconfig.Listener{ID: "h3", Protocol: "http3", Address: "127.0.0.1:" + strconv.Itoa(port), Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath}
	startGatewayListeners(t, daemon, []gwconfig.Listener{listener}, []protocol.Carrier{{Name: "http3", Port: port, TLS: true}})
	return daemon, port, cert
}

// Posts and room reads go over HTTP/3, and one QUIC connection keeps working
// after the client's address changes under it.
func TestHTTP3CarrierPostsReadsAndSurvivesAnAddressChange(t *testing.T) {
	daemon, port, cert := http3Fixture(t)
	relay := startUDPRelay(t, "127.0.0.1:"+strconv.Itoa(port))
	endpoint := fallback.Endpoint{Carrier: protocol.Carrier{Name: "http3", Port: relay.addr().Port, TLS: true}}
	c := &fallback.Client{Host: "127.0.0.1", Nick: "alice", TLSConfig: cert.Client, Endpoints: []fallback.Endpoint{endpoint}}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	receipt, err := c.Post(ctx, fallback.Message{Target: "#ops", Body: "over quic", RequestID: "h3-post-1"})
	if err != nil || receipt.Endpoint != endpoint || receipt.MessageID == "" {
		t.Fatalf("%+v %v", receipt, err)
	}
	if got := historyOf(t, daemon, "#ops", "over quic"); len(got) != 1 || got[0].ID != receipt.MessageID {
		t.Fatalf("history %+v", got)
	}
	reader, err := c.ReadRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if page, err := reader.Read(ctx, "#ops", "", 10); err != nil || len(page.Messages) != 1 || page.Messages[0].ID != receipt.MessageID {
		t.Fatalf("%+v %v", page, err)
	}
	relay.rebind(t)
	send(t, daemon, "bob", "#ops", "after the move")
	page, err := reader.Read(ctx, "#ops", receipt.MessageID, 10)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Message != "after the move" {
		t.Fatalf("after the address change: %+v %v", page, err)
	}
}

// With aircd behind TLS and its access token, a send whose native port is
// gone goes over HTTP/3 with the same certificate trust and credentials.
func TestSendFallsBackOverHTTP3(t *testing.T) {
	agentEnv(t)
	t.Setenv("AIRC_CARRIERS", "")
	cert := testtls.New(t)
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil { // the gateway reads credentials only from private directories
		t.Fatal(err)
	}
	tokenPath := filepath.Join(dir, "access.token")
	if err := admin.CreateToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	token, _ := admin.ReadToken(tokenPath)
	daemon := cliTestServerSetup(t, server.Config{TLSConfig: cert.Server, HistoryLimit: 64}, func(s *server.Server) error {
		if err := s.EnableAccess(token); err != nil {
			return err
		}
		return s.RestoreAccounts(filepath.Join(dir, "accounts.json"))
	})
	port := freeUDPPort(t)
	listener := gwconfig.Listener{ID: "h3", Protocol: "http3", Address: "127.0.0.1:" + strconv.Itoa(port), Enabled: true, TLSCertFile: cert.CertPath, TLSKeyFile: cert.KeyPath, PasswordFile: tokenPath}
	upstream := gwconfig.Upstream{Network: "tcp", Address: daemon, TLSServerName: "localhost", TLSCAFile: cert.CAPath, AccessTokenFile: tokenPath}
	startGatewayUpstream(t, upstream, []gwconfig.Listener{listener}, []protocol.Carrier{{Name: "http3", Port: port, TLS: true}})

	gone := "127.0.0.1:" + strconv.Itoa(closedTCPPort(t))
	sent, stderr, err := sendJSON(t, "--addr", gone, "--tls-ca", cert.CAPath, "--access-token-file", tokenPath, "--nick", "alice", "--channel", "#ops", "--carriers", "http3+tls:"+strconv.Itoa(port), "--message", "over http3")
	if err != nil || sent.Carrier != "http3+tls:"+strconv.Itoa(port) {
		t.Fatalf("%+v %v %s", sent, err, stderr)
	}
}

// Service readiness tells a listening UDP carrier from a closed port.
func TestServiceReadinessProbesUDPCarriers(t *testing.T) {
	l, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	address := l.LocalAddr().String()
	if err := dialCarrier(service.Carrier{Name: "http3", Address: address}); err != nil {
		t.Fatalf("listening: %v", err)
	}
	_ = l.Close()
	if err := dialCarrier(service.Carrier{Name: "http3", Address: address}); err == nil {
		t.Fatal("a closed UDP port looked ready")
	}
}

// freeUDPPort is a loopback UDP port nothing listens on.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	probe, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	return probe.LocalAddr().(*net.UDPAddr).Port
}
