package admin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenCreationAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "admin.token")
	if err := CreateToken(path); err != nil {
		t.Fatal(err)
	}
	first, err := ReadToken(path)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("credential permissions")
	}
	if err := CreateToken(path); err == nil {
		t.Fatal("overwrote credential")
	}
	second, _ := ReadToken(path)
	if first != second {
		t.Fatal("credential changed")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); err == nil {
		t.Fatal("accepted public credential")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"secret", first + "\nextra", first + "\r\n", first + " "} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadToken(path); err == nil {
			t.Fatal("accepted malformed credential")
		}
	}
}
