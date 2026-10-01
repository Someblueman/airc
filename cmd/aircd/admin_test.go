package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/server"
)

func TestConfigureAdminRequiresCredentialAndSeparateStorage(t *testing.T) {
	dir := t.TempDir()
	tokenPath := filepath.Join(dir, "admin.token")
	if err := admin.CreateToken(tokenPath); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][3]string{
		{"", filepath.Join(dir, "rules"), ""},
		{tokenPath, tokenPath, ""},
		{tokenPath, filepath.Join(dir, "history"), filepath.Join(dir, "history")},
		{tokenPath, filepath.Join(dir, "history.profiles.json"), filepath.Join(dir, "history")},
	} {
		if err := configureAdmin(server.New(server.Config{}), args[0], args[1], args[2]); err == nil {
			t.Fatal("accepted conflicting configuration", args)
		}
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Link(tokenPath, alias); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, alias, ""); err == nil {
		t.Fatal("accepted credential hard link")
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, "", filepath.Join(dir, "history")); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), "", "", ""); err != nil {
		t.Fatal(err)
	}
}
