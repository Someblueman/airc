package auth

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/pem"

	"golang.org/x/crypto/ssh"
)

// SFTPCredentials owns one Ed25519 client private key and one pinned SSH host
// public key. Close clears private material after all users have joined.
type SFTPCredentials struct {
	PrivateKey ed25519.PrivateKey
	HostKey    ssh.PublicKey
}

func (c *SFTPCredentials) Close() {
	if c != nil {
		clear(c.PrivateKey)
		c.PrivateKey = nil
		c.HostKey = nil
	}
}

// ReadSFTPCredentials accepts an unencrypted PKCS#8/OpenSSH Ed25519 private
// key and exactly one plain ssh-ed25519 authorized-key entry.
func ReadSFTPCredentials(privatePath, hostPath string) (SFTPCredentials, error) {
	var credentials SFTPCredentials
	privateData, err := keyFile(privatePath, true)
	if err != nil {
		return credentials, ErrCredential
	}
	block, rest := pem.Decode(privateData)
	if block == nil || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 ||
		block.Type != "PRIVATE KEY" && block.Type != "OPENSSH PRIVATE KEY" ||
		!bytes.HasPrefix(privateData, []byte("-----BEGIN PRIVATE KEY-----")) && !bytes.HasPrefix(privateData, []byte("-----BEGIN OPENSSH PRIVATE KEY-----")) {
		if block != nil {
			clear(block.Bytes)
		}
		clear(privateData)
		return credentials, ErrCredential
	}
	parsed, parseErr := ssh.ParseRawPrivateKey(privateData)
	clear(block.Bytes)
	clear(privateData)
	var privateKey ed25519.PrivateKey
	switch key := parsed.(type) {
	case ed25519.PrivateKey:
		privateKey = key
	case *ed25519.PrivateKey:
		if key != nil {
			privateKey = *key
		}
	}
	ok := privateKey != nil
	if parseErr != nil || !ok || len(privateKey) != ed25519.PrivateKeySize || !validEd25519Key(privateKey) {
		if privateKey != nil {
			clear(privateKey)
		}
		return credentials, ErrCredential
	}
	credentials.PrivateKey = append(ed25519.PrivateKey(nil), privateKey...)
	clear(privateKey)

	ready := false
	defer func() {
		if !ready {
			credentials.Close()
		}
	}()

	hostData, err := keyFile(hostPath, false)
	if err != nil {
		return SFTPCredentials{}, ErrCredential
	}
	defer clear(hostData)
	hostKey, _, options, rest, err := ssh.ParseAuthorizedKey(hostData)
	if err != nil || len(options) != 0 || len(bytes.TrimSpace(rest)) != 0 || hostKey == nil || hostKey.Type() != ssh.KeyAlgoED25519 {
		return SFTPCredentials{}, ErrCredential
	}
	if _, certificate := hostKey.(*ssh.Certificate); certificate {
		return SFTPCredentials{}, ErrCredential
	}
	cryptoKey, ok := hostKey.(ssh.CryptoPublicKey)
	if !ok {
		return SFTPCredentials{}, ErrCredential
	}
	publicKey, ok := cryptoKey.CryptoPublicKey().(ed25519.PublicKey)
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return SFTPCredentials{}, ErrCredential
	}
	credentials.HostKey = hostKey
	ready = true
	return credentials, nil
}

// validEd25519Key checks that a private key's public half matches its seed.
func validEd25519Key(key ed25519.PrivateKey) bool {
	if len(key) != ed25519.PrivateKeySize {
		return false
	}
	derived := ed25519.NewKeyFromSeed(key.Seed())
	defer clear(derived)
	return subtle.ConstantTimeCompare(derived[ed25519.SeedSize:], key[ed25519.SeedSize:]) == 1
}
