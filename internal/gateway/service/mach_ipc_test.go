//go:build darwin && arm64

package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/machipc"
)

func machIPCServiceConfig(t *testing.T, name string) config.Config {
	t.Helper()
	c, _, _, _ := natsServiceConfig(t)
	uid := uint32(os.Geteuid())
	root := filepath.Dir(c.StateDir)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(root, "mach-recipient.pem")
	if err := os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	publicPath := filepath.Join(root, "mach-reply.pem")
	if err := os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}), 0644); err != nil {
		t.Fatal(err)
	}
	c.RecipientKeys = []config.RecipientKey{{ID: "ingress", PrivateKeyFile: privatePath}}
	c.ReturnRoutes = []config.ReturnRoute{{
		ID: "mach-reply", Principal: c.Principals[0].ID, Protocol: "mach_ipc", Listener: "mach",
		Destination: "reply_port", RecipientKeyID: "reply", RecipientPublicKeyFile: publicPath,
	}}
	c.Listeners = []config.Listener{{
		ID: "mach", Protocol: "mach_ipc", Address: name, Enabled: true,
		Operations: []core.Operation{core.Send},
		MachIPC:    &config.MachIPC{Callers: []config.MachIPCCaller{{UID: &uid, Principal: c.Principals[0].ID}}},
	}}
	return c
}

func machIPCServiceName(t *testing.T) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return "com.example.airc.test." + hex.EncodeToString(suffix[:])
}

func TestMachIPCServiceReadyShutdownAndRebind(t *testing.T) {
	name := machIPCServiceName(t)
	c := machIPCServiceConfig(t, name)
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := Run(ctx, c, writer)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	joined := false
	t.Cleanup(func() {
		cancel()
		_ = reader.Close()
		if !joined {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("Mach IPC service did not join")
			}
		}
	})
	readyResult := make(chan error, 1)
	go func() {
		var ready struct {
			State     string `json:"state"`
			Listeners []struct {
				ID      string `json:"id"`
				Address string `json:"address"`
			} `json:"listeners"`
		}
		err := json.NewDecoder(reader).Decode(&ready)
		if err == nil && (ready.State != "ready" || len(ready.Listeners) != 1 || ready.Listeners[0].ID != "mach" || ready.Listeners[0].Address != name) {
			err = errors.New("incorrect Mach IPC readiness")
		}
		readyResult <- err
	}()
	select {
	case err := <-readyResult:
		if err != nil {
			t.Fatal("Mach IPC service readiness", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Mach IPC service did not become ready")
	}
	cancel()
	select {
	case err := <-done:
		joined = true
		if err != nil {
			t.Fatal("Mach IPC service shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Mach IPC service did not join after cancellation")
	}
	_ = reader.Close()
	listener, err := machipc.Listen(name)
	if err != nil {
		t.Fatalf("shutdown left bootstrap service registered: %v", err)
	}
	_ = listener.Close()
}

func TestMachIPCStartupFailureAfterBindRemovesRegistration(t *testing.T) {
	name := machIPCServiceName(t)
	c := machIPCServiceConfig(t, name)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	c.Listeners = append(c.Listeners, config.Listener{ID: "later", Protocol: "http", Enabled: true, Address: occupied.Addr().String(), Operations: []core.Operation{core.Send}})
	var output bytes.Buffer
	err = Run(t.Context(), c, &output)
	var listenErr *net.OpError
	if !errors.As(err, &listenErr) || listenErr.Op != "listen" || output.Len() != 0 {
		t.Fatalf("later listener bind failure reported ready or failed elsewhere: %v %s", err, output.String())
	}
	listener, err := machipc.Listen(name)
	if err != nil {
		t.Fatalf("startup rollback left bootstrap service registered: %v", err)
	}
	_ = listener.Close()
}

func TestMachIPCDuplicateBootstrapNamePreservesExistingOwner(t *testing.T) {
	name := machIPCServiceName(t)
	incumbent, err := machipc.Listen(name)
	if err != nil {
		t.Fatal("prepare existing bootstrap owner", err)
	}
	defer incumbent.Close()
	c := machIPCServiceConfig(t, name)
	var output bytes.Buffer
	err = Run(t.Context(), c, &output)
	if !errors.Is(err, os.ErrExist) || output.Len() != 0 {
		t.Fatalf("duplicate bootstrap name claimed readiness or failed unexpectedly: %v %s", err, output.String())
	}
	duplicate, err := machipc.Listen(name)
	if err == nil {
		_ = duplicate.Close()
		t.Fatal("duplicate startup removed or replaced the existing bootstrap owner")
	}
	_ = incumbent.Close()
	rebound, err := machipc.Listen(name)
	if err != nil {
		t.Fatalf("existing bootstrap owner did not release its service name: %v", err)
	}
	_ = rebound.Close()
}
