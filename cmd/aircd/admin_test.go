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
	history := filepath.Join(dir, "history")
	for _, args := range [][]string{
		{"", filepath.Join(dir, "rules")}, // moderation without a credential
		{tokenPath, ""},                   // credential without a moderation file
		{tokenPath, tokenPath},            // moderation in the credential file
		{tokenPath, history, history},     // moderation in the history file
		{tokenPath, history + ".profiles.json", history, history + ".topics.json", history + ".profiles.json"}, // moderation in another state file
	} {
		if err := configureAdmin(server.New(server.Config{}), args[0], args[1], args[2:]...); err == nil {
			t.Fatal("accepted conflicting configuration", args)
		}
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Link(tokenPath, alias); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, alias); err == nil {
		t.Fatal("accepted credential hard link")
	}
	if err := configureAdmin(server.New(server.Config{}), tokenPath, filepath.Join(dir, "moderation.json"), history, history+".topics.json"); err != nil {
		t.Fatal(err)
	}
	if err := configureAdmin(server.New(server.Config{}), "", ""); err != nil {
		t.Fatal(err)
	}
}
