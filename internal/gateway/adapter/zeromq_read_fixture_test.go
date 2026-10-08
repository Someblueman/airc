//go:build darwin || linux

package adapter

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	jose "github.com/go-jose/go-jose/v4"
)

func zeroMQReadFixture(t *testing.T) (amqpAdmissionFixture, jose.Encrypter) {
	t.Helper()
	f := newAMQPAdmissionFixture(t)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(f.c.StateDir), "decrypt.pem")
	if err = os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	f.c.RecipientKeys = []config.RecipientKey{{ID: "decrypt", PrivateKeyFile: path}}
	f.v, err = auth.New(f.c)
	if err != nil {
		t.Fatal(err)
	}
	f.d, err = dispatch.New(f.s, f.v, f.reader, f.cursors, f.route().Operations)
	if err != nil {
		t.Fatal(err)
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: &key.PublicKey, KeyID: "decrypt"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return f, encrypter
}
func zeroMQEncryptedRequest(t *testing.T, f amqpAdmissionFixture, e jose.Encrypter, r core.Request) string {
	t.Helper()
	sealed, err := e.Encrypt(redisSignedRequest(t, f, r, 0))
	if err != nil {
		t.Fatal(err)
	}
	token, err := sealed.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return token
}
