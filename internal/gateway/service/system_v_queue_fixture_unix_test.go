//go:build (darwin && arm64) || (linux && (arm64 || amd64))

package service

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/store"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func systemVPacketFixture(t *testing.T) (config.Config, *store.Store, config.Listener, int32) {
	t.Helper()
	c, journal, key := systemVIngressFixture(t)
	l := config.Listener{ID: "sysv", Protocol: "system_v_queue", Address: strconv.FormatInt(int64(key), 10)}
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
	_, other, response := systemVIngressFixture(t)
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if response == key {
		t.Fatal("fixture queue-key collision")
	}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "reply", Protocol: "system_v_queue", Listener: l.ID, Principal: "device", Destination: strconv.FormatInt(int64(response), 10), RecipientKeyID: "recipient", RecipientPublicKeyFile: recipientPath}}
	return c, journal, l, response
}
