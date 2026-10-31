package main

import (
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
)

func TestConfigureAdminNeedsAReadableCredential(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "admin.token")
	moderation := filepath.Join(dir, "moderation.json")
	if err := configureAdmin(server.New(server.Config{}), "", ""); err != nil {
		t.Fatalf("without a credential administration stays off: %v", err)
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, moderation); err == nil {
		t.Fatal("accepted a missing credential file")
	}
	if err := admin.CreateToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, moderation); err != nil {
		t.Fatal(err)
	}
}
