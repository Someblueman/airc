//go:build darwin || linux

package service

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	jose "github.com/go-jose/go-jose/v4"
)

func zeroMQServiceConfig(t *testing.T) (config.Config, jose.Signer, jose.Encrypter) {
	t.Helper()
	c, _, signer, _ := signedServiceConfig(t)
	c.Listeners = []config.Listener{{ID: "zeromq", Protocol: "zeromq", Enabled: true, Address: "127.0.0.1:0", Operations: []core.Operation{core.Send}}}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(c.StateDir), "decrypt.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	c.RecipientKeys = []config.RecipientKey{{ID: "decrypt", PrivateKeyFile: path}}
	e, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &key.PublicKey, KeyID: "decrypt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c, signer, e
}
