package auth

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

type verificationKey struct {
	public   ed25519.PublicKey
	identity Identity
}
type messageProfiles struct {
	audience    string
	maxLifetime int64
	signing     map[string]verificationKey
	recipients  map[string]*ecdsa.PrivateKey
}

type publicJWK struct {
	Type      string `json:"kty"`
	Curve     string `json:"crv"`
	X         string `json:"x"`
	Algorithm string `json:"alg,omitempty"`
	Use       string `json:"use,omitempty"`
	ID        string `json:"kid,omitempty"`
}

func keyFile(path string, private bool) ([]byte, error) {
	open := openPublic
	if private {
		open = openPrivate
	}
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(data) > 8192 {
		clear(data)
		return nil, ErrCredential
	}
	return data, nil
}

func (v *Verifier) loadProfiles(c config.Config) error {
	v.profiles = messageProfiles{audience: c.Realm, maxLifetime: c.Retry.LifetimeSeconds, signing: map[string]verificationKey{}, recipients: map[string]*ecdsa.PrivateKey{}}
	owners := map[string]string{}
	for j, p := range c.Principals {
		for _, entry := range p.SigningKeys {
			data, err := keyFile(entry.PublicKeyFile, false)
			if err != nil {
				return err
			}
			var key publicJWK
			err = jsoninput.Decode(data, &key, 8192)
			if err != nil || key.Type != "OKP" || key.Curve != "Ed25519" || key.Algorithm != "" && key.Algorithm != "EdDSA" || key.Use != "" && key.Use != "sig" || key.ID != "" && key.ID != entry.ID {
				return ErrCredential
			}
			x, err := base64.RawURLEncoding.Strict().DecodeString(key.X)
			if err != nil || len(x) != ed25519.PublicKeySize || base64.RawURLEncoding.EncodeToString(x) != key.X {
				return ErrCredential
			}
			if owner, ok := owners[string(x)]; ok && owner != p.ID {
				return ErrCredential
			}
			owners[string(x)] = p.ID
			v.profiles.signing[entry.ID] = verificationKey{public: ed25519.PublicKey(x), identity: Identity{principal: &v.principals[j]}}
		}
	}
	for _, entry := range c.RecipientKeys {
		data, err := keyFile(entry.PrivateKeyFile, true)
		if err != nil {
			return err
		}
		block, rest := pem.Decode(data)
		startsWithKey := bytes.HasPrefix(data, []byte("-----BEGIN PRIVATE KEY-----"))
		clear(data)
		if !startsWithKey || block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(rest) != 0 {
			return ErrCredential
		}
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		clear(block.Bytes)
		key, ok := parsed.(*ecdsa.PrivateKey)
		if err != nil || !ok || key.Curve != elliptic.P256() {
			return ErrCredential
		}
		v.profiles.recipients[entry.ID] = key
	}
	return nil
}
