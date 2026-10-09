package service

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestSCTPLoadsFixedConnectionResponseProtection(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(signer)
	if err != nil {
		t.Fatal(err)
	}
	signerPath := filepath.Join(root, "signer.pem")
	if err := os.WriteFile(signerPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER}), 0600); err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipientDER, err := x509.MarshalPKIXPublicKey(&recipient.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	recipientPath := filepath.Join(root, "recipient.pem")
	if err := os.WriteFile(recipientPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: recipientDER}), 0644); err != nil {
		t.Fatal(err)
	}
	c := config.Config{
		Realm:              "realm",
		ResponseSigningKey: &config.ResponseSigningKey{ID: "response", PrivateKeyFile: signerPath},
		Principals:         []config.Principal{{ID: "principal", Binding: "binding"}},
		ReturnRoutes: []config.ReturnRoute{{
			ID: "reply", Principal: "principal", Protocol: "sctp", Listener: "ordered",
			Destination: "connection", RecipientKeyID: "reader", RecipientPublicKeyFile: recipientPath,
		}},
	}
	listener := config.Listener{ID: "ordered", Protocol: "sctp"}
	protection, err := loadConnectionResponseProtection(c, listener)
	if err != nil || protection == nil {
		t.Fatalf("load SCTP response route: protection=%v err=%v", protection, err)
	}
	c.ReturnRoutes[0].Protocol = "coap_tcp"
	if _, err := loadConnectionResponseProtection(c, listener); err == nil {
		t.Fatal("SCTP loaded a response route from a different protocol")
	}
	listener.Protocol = "sctp_unordered"
	c.ReturnRoutes[0].Protocol = "sctp_unordered"
	if _, err := loadConnectionResponseProtection(c, listener); err != nil {
		t.Fatalf("load unordered SCTP response route: %v", err)
	}
	c.ReturnRoutes[0].Protocol = "sctp"
	if _, err := loadConnectionResponseProtection(c, listener); err == nil {
		t.Fatal("unordered SCTP loaded an ordered-mode response route")
	}
}
