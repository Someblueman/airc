// udp-lab provisions an isolated UDP test pair and prepares encrypted requests.
// It deliberately uses the existing udp-client for all gateway traffic.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
	jose "github.com/go-jose/go-jose/v4"
)

const realm = "vps-udp-lab"

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("use init, register, or envelope (see docs/gateway/vps-test.md)")
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	dir := fs.String("dir", "", "absolute new output directory (init) or existing server/client directory")
	serverDir := fs.String("server-dir", "", "absolute deployment directory on the server (init only)")
	listen := fs.String("listen", "0.0.0.0:9001", "numeric gateway UDP listen address (init only)")
	returnAddress := fs.String("return", "", "numeric client's reachable fixed UDP mailbox (init only)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 || !filepath.IsAbs(*dir) || filepath.Clean(*dir) != *dir {
		return errors.New("a clean absolute --dir is required, with no positional arguments")
	}
	switch args[0] {
	case "init":
		return provision(*dir, *serverDir, *listen, *returnAddress)
	case "register":
		token, err := auth.ReadToken(filepath.Join(*dir, "account.token"))
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client, err := irc.DialContext(ctx, irc.Config{Network: "unix", Addr: filepath.Join(*dir, "native.sock"), Nick: "udp-agent", IdentityToken: token, CreateAccount: true, Ephemeral: true, ReadTimeout: 5 * time.Second, WriteTimeout: time.Second})
		if err != nil {
			return err
		}
		return client.Close()
	case "envelope":
		data, err := io.ReadAll(io.LimitReader(in, core.MaxEnvelopeBytes+1))
		if err != nil {
			return err
		}
		token, err := envelope(*dir, data, time.Now())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, token)
		return err
	default:
		return errors.New("unknown command; use init, register, or envelope")
	}
}

func provision(dir, serverDir, listen, returnAddress string) error {
	if !filepath.IsAbs(serverDir) || filepath.Clean(serverDir) != serverDir {
		return errors.New("init requires a clean absolute --server-dir")
	}
	c := config.Defaults()
	c.Version, c.Realm, c.StateDir = 1, realm, filepath.Join(serverDir, "journal")
	c.CursorKeyFile = filepath.Join(serverDir, "cursor.token")
	ops := []core.Operation{core.Send, core.ReceiptGet, core.HistoryRead}
	c.Bindings = []config.Binding{{ID: "native", Network: "unix", Address: filepath.Join(serverDir, "native.sock"), Account: "udp-agent", AccountTokenFile: filepath.Join(serverDir, "account.token")}}
	c.Principals = []config.Principal{{ID: "agent", Binding: "native", Operations: ops, Targets: []core.Target{{Kind: "room", Name: "#udp-lab"}}, SigningKeys: []config.SigningKey{{ID: "agent-signing", PublicKeyFile: filepath.Join(serverDir, "agent-signing.json")}}}}
	c.RecipientKeys = []config.RecipientKey{{ID: "gateway-recipient", PrivateKeyFile: filepath.Join(serverDir, "recipient.pem")}}
	c.ResponseSigningKey = &config.ResponseSigningKey{ID: "gateway-signing", PrivateKeyFile: filepath.Join(serverDir, "signing.pem")}
	c.Listeners = []config.Listener{{ID: "udp", Protocol: "udp", Address: listen, Enabled: true, Operations: ops}}
	c.ReturnRoutes = []config.ReturnRoute{{ID: "agent-return", Principal: "agent", Protocol: "udp", Listener: "udp", Destination: returnAddress, RecipientKeyID: "agent-recipient", RecipientPublicKeyFile: filepath.Join(serverDir, "agent-recipient.pem")}}
	if err := c.Validate(); err != nil {
		return err
	}
	// Exclusive directory creation prevents accidental credential rotation.
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	server, client := filepath.Join(dir, "server"), filepath.Join(dir, "client")
	for _, path := range []string{server, client, filepath.Join(server, "journal")} {
		if err := os.Mkdir(path, 0700); err != nil {
			return err
		}
	}
	for _, name := range []string{"account.token", "cursor.token"} {
		if err := auth.CreateCredential(filepath.Join(server, name)); err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{client, server}, {server, client}} {
		pub, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		if err := writeKey(filepath.Join(pair[0], "signing.pem"), private, true); err != nil {
			return err
		}
		if pair[0] == client {
			jwk := map[string]string{"kty": "OKP", "crv": "Ed25519", "x": base64.RawURLEncoding.EncodeToString(pub)}
			if err := writeJSON(filepath.Join(server, "agent-signing.json"), jwk); err != nil {
				return err
			}
		} else if err := writeKey(filepath.Join(client, "gateway-signing.pem"), pub, false); err != nil {
			return err
		}
		recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		if err := writeKey(filepath.Join(pair[0], "recipient.pem"), recipient, true); err != nil {
			return err
		}
		name := "gateway-recipient.pem"
		if pair[0] == client {
			name = "agent-recipient.pem"
		}
		if err := writeKey(filepath.Join(pair[1], name), &recipient.PublicKey, false); err != nil {
			return err
		}
	}
	return writeJSON(filepath.Join(server, "gateway.json"), c)
}

func writeKey(path string, key any, private bool) error {
	var der []byte
	var err error
	kind := "PUBLIC KEY"
	if private {
		der, err = x509.MarshalPKCS8PrivateKey(key)
		kind = "PRIVATE KEY"
	} else {
		der, err = x509.MarshalPKIXPublicKey(key)
	}
	if err != nil {
		return err
	}
	defer clear(der)
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0600)
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}

func envelope(dir string, data []byte, now time.Time) (string, error) {
	r, err := core.Decode(data)
	if err != nil {
		return "", err
	}
	if r.Operation != core.Send && r.Operation != core.ReceiptGet && r.Operation != core.HistoryRead {
		return "", errors.New("this lab allows only send, receipt.get, and history.read")
	}
	issued, expiry := now.Unix(), now.Add(120*time.Second).Unix()
	if r.Operation == core.Send {
		if r.IssuedAt != nil {
			issued, expiry = *r.IssuedAt, *r.ExpiresAt
		} else {
			r.IssuedAt, r.ExpiresAt = &issued, &expiry
		}
	}
	key, err := auth.ReadResponseSigningKey(filepath.Join(dir, "signing.pem"))
	if err != nil {
		return "", err
	}
	defer clear(key)
	recipient, err := auth.ReadResponseRecipientKey(filepath.Join(dir, "gateway-recipient.pem"))
	if err != nil {
		return "", err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, (&jose.SignerOptions{}).WithHeader("kid", "agent-signing"))
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(auth.MessageEnvelope{Version: 1, Audience: realm, IssuedAt: issued, ExpiresAt: expiry, Request: r})
	if err != nil {
		return "", err
	}
	signed, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return "", err
	}
	encrypter, err := jose.NewEncrypter(jose.A256GCM, jose.Recipient{Algorithm: jose.ECDH_ES, Key: recipient, KeyID: "gateway-recipient"}, nil)
	if err != nil {
		return "", err
	}
	encrypted, err := encrypter.Encrypt([]byte(compact))
	if err != nil {
		return "", err
	}
	return encrypted.CompactSerialize()
}
