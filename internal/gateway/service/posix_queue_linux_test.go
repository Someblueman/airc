//go:build linux && (arm64 || amd64)

package service

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func posixPacketFixture(t *testing.T) (config.Config, *store.Store, config.Listener, string) {
	t.Helper()
	c, journal, l := posixIngressFixture(t)
	root := filepath.Dir(c.StateDir)
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&recipient.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	signingPath, recipientPath := filepath.Join(root, "response.pem"), filepath.Join(root, "recipient.pem")
	if err := os.WriteFile(signingPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}), 0600); err != nil {
		t.Fatal(err)
	}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "response", PrivateKeyFile: signingPath}
	c.Principals = []config.Principal{{ID: "device", Binding: "backend"}}
	response := l.Address + "-reply"
	c.ReturnRoutes = []config.ReturnRoute{{ID: "reply", Protocol: "posix_mqueue", Listener: l.ID, Principal: "device", Destination: response, RecipientKeyID: "recipient", RecipientPublicKeyFile: recipientPath}}
	return c, journal, l, response
}

func TestPOSIXQueueOwnerMissingResponsePreservesIngressAbsence(t *testing.T) {
	c, journal, l, _ := posixPacketFixture(t)
	owner, err := newPOSIXQueueOwner(t.Context(), c, l, journal, &dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1))
	if err == nil || owner != nil {
		t.Fatal("missing response accepted", err)
	}
	if q, err := adapter.OpenPOSIXQueue(l.Address, 1); !errors.Is(err, os.ErrNotExist) {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("ingress created before response verification", err)
	}
	if _, err := journal.LoadPOSIXQueueOwnership(t.Context(), l.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed startup registered ingress", err)
	}
}

func TestPOSIXQueueOwnerRoutesAndJoinedClose(t *testing.T) {
	c, journal, l, name := posixPacketFixture(t)
	response, err := adapter.CreatePOSIXQueue(name, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Close()
	owner, err := newPOSIXQueueOwner(t.Context(), c, l, journal, &dispatch.Dispatcher{}, make(chan struct{}, 1), make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer owner.close()
	ingress, err := adapter.OpenPOSIXQueue(l.Address, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer ingress.Close()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := ingress.WriteMessage(ctx, []byte("whole ingress fragment")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, adapter.POSIXQueueMessageBytes)
	n, addr, err := owner.conn.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "whole ingress fragment" || addr.String() != l.Address {
		t.Fatal("ingress packet mapping", n, addr, err)
	}
	if _, err := owner.conn.WriteTo([]byte("whole response fragment"), &adapter.POSIXQueueAddr{Name: name}); err != nil {
		t.Fatal(err)
	}
	n, _, err = response.ReadMessage(ctx, buf)
	if err != nil || string(buf[:n]) != "whole response fragment" {
		t.Fatal("fixed response mapping", n, err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := owner.conn.ReadFrom(buf); done <- err }()
	if err := owner.close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed read succeeded")
		}
	case <-ctx.Done():
		t.Fatal("read did not join close")
	}
	if q, err := adapter.OpenPOSIXQueue(l.Address, 1); !errors.Is(err, os.ErrNotExist) {
		if q != nil {
			_ = q.Close()
		}
		t.Fatal("owned ingress survived close", err)
	}
	opened, err := adapter.OpenPOSIXQueue(name, 1)
	if err != nil {
		t.Fatal("external response removed", err)
	}
	defer opened.Close()
	if opened.Identity() != response.Identity() {
		t.Fatal("external response replaced")
	}
}
