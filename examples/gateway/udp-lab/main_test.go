package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/config"
)

func TestProvisionAndEnvelopeAuthenticateWithGateway(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "lab")
	server := filepath.Join(dir, "server")
	if err := provision(dir, server, "127.0.0.1:9001", "127.0.0.1:49152"); err != nil {
		t.Fatal(err)
	}
	if err := provision(dir, server, "127.0.0.1:9001", "127.0.0.1:49152"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing lab must not rotate credentials: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(server, "gateway.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.CheckFiles(); err != nil {
		t.Fatal(err)
	}
	v, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Second)
	for _, request := range []string{
		`{"version":1,"request_id":"post","operation":"send","target":{"kind":"room","name":"#udp-lab"},"message":"remote hello 🙂"}`,
		`{"version":1,"request_id":"receipt","operation":"receipt.get","lookup_id":"post"}`,
		`{"version":1,"request_id":"history","operation":"history.read","target":{"kind":"room","name":"#udp-lab"},"start":"retained","limit":10}`,
	} {
		token, err := envelope(filepath.Join(dir, "client"), []byte(request), now, "agent-signing")
		if err != nil {
			t.Fatal(err)
		}
		message, err := v.AuthenticateEncrypted(token, now)
		if err != nil || message.Identity.ID() != "agent" {
			t.Fatalf("generated envelope must authenticate as agent: %v", err)
		}
		if _, err := v.AuthenticateEncrypted(token, now.Add(4*time.Minute)); err == nil {
			t.Fatal("expired lab envelope authenticated")
		}
	}
	// A separate account uses its own configured key ID, not the first client's.
	c.Principals[0].ID = "guest"
	c.Principals[0].SigningKeys[0].ID = "guest-signing"
	c.ReturnRoutes[0].Principal = "guest"
	guest, err := auth.New(c)
	if err != nil {
		t.Fatal(err)
	}
	request := []byte(`{"version":1,"request_id":"guest-receipt","operation":"receipt.get","lookup_id":"post"}`)
	token, err := envelope(filepath.Join(dir, "client"), request, now, "guest-signing")
	if err != nil {
		t.Fatal(err)
	}
	message, err := guest.AuthenticateEncrypted(token, now)
	if err != nil || message.Identity.ID() != "guest" {
		t.Fatalf("configured signing ID must resolve the guest: %v", err)
	}
	if _, err := v.AuthenticateEncrypted(token, now); err == nil {
		t.Fatal("unconfigured guest signing ID authenticated as the original client")
	}
	if err := os.Chmod(filepath.Join(dir, "client", "signing.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := envelope(filepath.Join(dir, "client"), request, now, "guest-signing"); err == nil {
		t.Fatal("insecure signing key accepted")
	}
}
