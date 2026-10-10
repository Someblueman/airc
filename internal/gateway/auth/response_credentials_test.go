package auth

import (
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
)

func writeResponseCredential(t *testing.T, name string, data []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.Chmod(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func responseSigningPEM(t *testing.T, key ed25519.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func responseRecipientPEM(t *testing.T, key *ecdsa.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func TestResponseCredentialLoaders(t *testing.T) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signingPath := writeResponseCredential(t, "signing.pem", responseSigningPEM(t, signing), 0600)
	recipientPath := writeResponseCredential(t, "recipient.pem", responseRecipientPEM(t, &recipient.PublicKey), 0644)
	gotSigning, err := ReadResponseSigningKey(signingPath)
	if err != nil || !gotSigning.Public().(ed25519.PublicKey).Equal(signing.Public()) {
		t.Fatalf("signing key error=%v", err)
	}
	gotRecipient, err := ReadResponseRecipientKey(recipientPath)
	wantRecipient, marshalErr := recipient.PublicKey.Bytes()
	var gotRecipientBytes []byte
	var bytesErr error
	if gotRecipient != nil {
		gotRecipientBytes, bytesErr = gotRecipient.Bytes()
	}
	if err != nil || marshalErr != nil || bytesErr != nil || string(gotRecipientBytes) != string(wantRecipient) {
		t.Fatalf("recipient key error=%v", err)
	}
}

func TestResponseCredentialLoadersRejectInvalidFiles(t *testing.T) {
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	badSigning := append(ed25519.PrivateKey(nil), signing...)
	badSigning[ed25519.SeedSize] ^= 1
	if validSigningKey(badSigning) {
		t.Fatal("inconsistent signing key accepted by validator")
	}
	p384, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		data []byte
		mode os.FileMode
		load func(string) error
	}{
		{"wrong-signing-type", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")}), 0600, func(p string) error { _, e := ReadResponseSigningKey(p); return e }},
		{"wrong-recipient-curve", responseRecipientPEM(t, &p384.PublicKey), 0644, func(p string) error { _, e := ReadResponseRecipientKey(p); return e }},
		{"truncated", []byte("-----BEGIN PUBLIC KEY-----\n"), 0644, func(p string) error { _, e := ReadResponseRecipientKey(p); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeResponseCredential(t, "key", tc.data, tc.mode)
			if !errors.Is(tc.load(path), ErrCredential) {
				t.Fatalf("accepted invalid credential")
			}
		})
	}

	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := writeResponseCredential(t, "unsafe", responseRecipientPEM(t, &recipient.PublicKey), 0622)
	if _, err := ReadResponseRecipientKey(path); !errors.Is(err, ErrCredential) {
		t.Fatalf("unsafe permissions error=%v", err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadResponseRecipientKey(link); !errors.Is(err, ErrCredential) {
		t.Fatalf("symlink error=%v", err)
	}
}
