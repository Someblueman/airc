//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/sysvqueue"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSystemVQueueServiceReadyAndJoinedRestart(t *testing.T) {
	c, journal, l, name := systemVPacketFixture(t)
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	full := directoryServiceConfig(t)
	c.StateDir, c.Bindings, c.Principals = full.StateDir, full.Bindings, full.Principals
	l.Enabled = true
	c.Listeners = []config.Listener{l}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(filepath.Dir(c.StateDir), "ingress.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	c.RecipientKeys = []config.RecipientKey{{ID: "ingress", PrivateKeyFile: keyPath}}
	c.CursorKeyFile = filepath.Join(filepath.Dir(c.StateDir), "cursor.key")
	if err := auth.CreateCredential(c.CursorKeyFile); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	failedCtx, failedCancel := context.WithTimeout(t.Context(), 5*time.Second)
	if err := Run(failedCtx, c, &output); err == nil {
		failedCancel()
		t.Fatal("service accepted absent provisioned response")
	}
	failedCancel()
	if output.Len() != 0 {
		t.Fatal("failed startup published readiness", output.String())
	}
	if q, err := sysvqueue.Open(mustSystemVKey(t, l.Address)); !errors.Is(err, os.ErrNotExist) {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("failed startup created ingress", err)
	}
	response, err := sysvqueue.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Close()
	for range 2 {
		ctx, cancel := context.WithCancel(t.Context())
		reader, writer := io.Pipe()
		done := make(chan error, 1)
		go func() { err := Run(ctx, c, writer); _ = writer.CloseWithError(err); done <- err }()
		t.Cleanup(func() { cancel(); _ = reader.Close() })
		ready := make(chan error, 1)
		go func() {
			var state struct {
				State     string
				Listeners []struct{ ID, Address string }
			}
			err := json.NewDecoder(reader).Decode(&state)
			if err == nil && (state.State != "ready" || len(state.Listeners) != 1 || state.Listeners[0].Address != l.Address) {
				err = errors.New("incorrect queue readiness")
			}
			ready <- err
		}()
		select {
		case err := <-ready:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("startup did not finish")
		}
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("service did not join")
		}
		_ = reader.Close()
		if q, err := sysvqueue.Open(mustSystemVKey(t, l.Address)); !errors.Is(err, os.ErrNotExist) {
			if q != nil {
				_ = q.Close()
			}
			t.Fatal("joined service retained ingress", err)
		}
		q, err := sysvqueue.Open(name)
		if err != nil {
			t.Fatal("service removed response queue", err)
		}
		if q.Identity() != response.Identity() {
			t.Fatal("restart replaced response lifecycle")
		}
		_ = q.Close()
	}
}
