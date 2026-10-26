package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/testfs"
)

// gatewayListener, gatewayUpstream and gatewayConfig are the gateway's
// configuration as its JSON reads it (gateway/internal/config). The gateway
// is its own module and runs here as its own process, built from that
// module, so this package links none of its carriers.
type gatewayListener struct {
	ID           string   `json:"id"`
	Protocol     string   `json:"protocol"`
	Address      string   `json:"address"`
	Enabled      bool     `json:"enabled"`
	Operations   []string `json:"operations"`
	TLSCertFile  string   `json:"tls_cert_file,omitempty"`
	TLSKeyFile   string   `json:"tls_key_file,omitempty"`
	PasswordFile string   `json:"password_file,omitempty"`
}

type gatewayUpstream struct {
	Network          string `json:"network"`
	Address          string `json:"address"`
	AccessTokenFile  string `json:"access_token_file,omitempty"`
	GatewayTokenFile string `json:"gateway_token_file,omitempty"`
	TLSServerName    string `json:"tls_server_name,omitempty"`
	TLSCAFile        string `json:"tls_ca_file,omitempty"`
}

type gatewayConfig struct {
	Version       int               `json:"version"`
	Realm         string            `json:"realm"`
	StateDir      string            `json:"state_dir"`
	CursorKeyFile string            `json:"cursor_key_file"`
	Upstream      *gatewayUpstream  `json:"upstream"`
	Carriers      []string          `json:"carriers,omitempty"`
	Listeners     []gatewayListener `json:"listeners"`
}

// Only the immutable gateway executable is shared, as nativeCLI is.
var gatewayFixture struct {
	once   sync.Once
	root   string
	binary string
	err    error
}

// gatewayBinary builds airc-gateway from the gateway module once per test
// binary.
func gatewayBinary(t testing.TB) string {
	t.Helper()
	gatewayFixture.once.Do(func() {
		gatewayFixture.root, gatewayFixture.binary, gatewayFixture.err = buildGateway()
	})
	if gatewayFixture.err != nil {
		t.Fatal(gatewayFixture.err)
	}
	return gatewayFixture.binary
}

func buildGateway() (string, string, error) {
	root, err := os.MkdirTemp("", "airc-test-gateway-")
	if err != nil {
		return "", "", err
	}
	binary := filepath.Join(root, "airc-gateway")
	// A cold build of the gateway takes a few minutes; the bound only
	// catches a hung toolchain.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/airc-gateway")
	build.Dir = "../../gateway"
	if output, err := build.CombinedOutput(); err != nil {
		_ = os.RemoveAll(root)
		return "", "", fmt.Errorf("build airc-gateway: %w\n%s", err, output)
	}
	if err := os.Chmod(binary, 0500); err != nil {
		_ = os.RemoveAll(root)
		return "", "", err
	}
	return root, binary, nil
}

// createGatewayCredential has the gateway write a credential file, as
// airc-gateway credential init does for an operator.
func createGatewayCredential(t testing.TB, path string) {
	t.Helper()
	if output, err := exec.Command(gatewayBinary(t), "credential", "init", "--file", path).CombinedOutput(); err != nil {
		t.Fatalf("credential init: %v\n%s", err, output)
	}
}

// startGatewayListeners runs a gateway with the given listeners.
func startGatewayListeners(t *testing.T, upstream string, listeners []gatewayListener, carriers []protocol.Carrier) {
	t.Helper()
	startGatewayUpstream(t, gatewayUpstream{Network: "tcp", Address: upstream}, listeners, carriers)
}

// startGatewayUpstream runs a gateway process reaching aircd as upstream
// describes, and stops it when the test ends. Settings left out of the
// configuration take the gateway's defaults.
func startGatewayUpstream(t *testing.T, upstream gatewayUpstream, listeners []gatewayListener, carriers []protocol.Carrier) {
	t.Helper()
	binary := gatewayBinary(t)
	root, err := testfs.MkdirTemp("airc-fallback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	c := gatewayConfig{Version: 1, Realm: "cli-fallback", StateDir: filepath.Join(root, "state"), CursorKeyFile: filepath.Join(root, "cursor.key"), Upstream: &upstream}
	if err := os.Mkdir(c.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	createGatewayCredential(t, c.CursorKeyFile)
	for _, carrier := range carriers {
		c.Carriers = append(c.Carriers, carrier.String())
	}
	for _, l := range listeners {
		if l.Operations == nil {
			l.Operations = []string{} // every operation, as IRC does
		}
		c.Listeners = append(c.Listeners, l)
	}
	data, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "gateway.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "serve", "--file", path)
	cmd.Env = append(os.Environ(), "HOME="+root, "XDG_CONFIG_HOME="+root, "XDG_STATE_HOME="+root)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(stdout)
	_, readErr := reader.ReadBytes('\n') // the ready record
	done := make(chan error, 1)
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		done <- cmd.Wait()
	}()
	if readErr != nil {
		_ = cmd.Process.Kill()
		t.Fatalf("gateway did not start: %v %v\n%s", readErr, <-done, stderr.String())
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("gateway stopped with %v\n%s", err, stderr.String())
			}
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Error("gateway did not stop")
		}
	})
}
