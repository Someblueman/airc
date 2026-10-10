//go:build unix

package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/core"
)

func TestMatrixOwnerPrivateTokenAndVerifierGrants(t *testing.T) {
	c, _ := fixtureConfig(t)
	v, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(privateTemp(t), "matrix-token")
	b := config.MatrixBridge{ID: "matrix", Address: "127.0.0.1:8448", TLSServerName: "localhost", Account: "@bridge:localhost", AccessTokenFile: path, FreshnessSeconds: 60, Routes: []config.MatrixRoute{{Author: "@alice:localhost", Room: "!room", Principal: c.Principals[0].ID, Target: c.Principals[0].Targets[0], Operations: []core.Operation{core.Send}}}}
	for _, token := range []string{"abc_123", "", "bad\n", "bad\rheader", "abc=def", "=", strings.Repeat("x", 1025)} {
		if err = os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := NewMatrixServer(v, c, b)
		if token != "abc_123" {
			if err == nil {
				s.Close()
				t.Fatal("bad token accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		g := s.grants[[2]string{"@alice:localhost", "!room"}]
		if !v.OwnsIdentity(g.identity) || g.target != b.Routes[0].Target {
			t.Fatal("wrong verifier authority")
		}
		retained := s.token
		s.Close()
		for _, c := range retained {
			if c != 0 {
				t.Fatal("token retained")
			}
		}
	}
	if err = os.WriteFile(path, []byte("abc"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if s, err := NewMatrixServer(v, c, b); err == nil {
		s.Close()
		t.Fatal("public token accepted")
	}
}
