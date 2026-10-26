//go:build unix

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestICECredentialBoundedProtectedText(t *testing.T) {
	path := filepath.Join(privateTemp(t), "turn.secret")
	for _, value := range []string{"TURN password with spaces", "secret-value\n", "", "one\ntwo", "bad\x00", strings.Repeat("x", 4097), "\xff"} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := ReadICECredential(path)
		valid := value == "TURN password with spaces" || value == "secret-value\n"
		if valid && (err != nil || got != strings.TrimSuffix(value, "\n")) || !valid && err == nil {
			t.Fatalf("credential validity=%v got length=%d err=%v", valid, len(got), err)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadICECredential(path); err == nil {
		t.Fatal("accepted readable private credential")
	}
}
