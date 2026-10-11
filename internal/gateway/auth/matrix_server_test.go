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

func TestMatrixOwnerPrivateTokenAndRoutes(t *testing.T) {
	c, _ := bridgeFixtureConfig(t)
	var err error
	path := filepath.Join(privateTemp(t), "matrix-token")
	b := config.MatrixBridge{ID: "matrix", Address: "127.0.0.1:8448", TLSServerName: "localhost", Account: "@bridge:localhost", AccessTokenFile: path, FreshnessSeconds: 60, Routes: []config.MatrixRoute{{Room: "!room", Target: core.Target{Kind: "room", Name: "#Allowed"}, Operations: []core.Operation{core.Send}}}}
	for _, token := range []string{"abc_123", "", "bad\n", "bad\rheader", "abc=def", "=", strings.Repeat("x", 1025)} {
		if err = os.WriteFile(path, []byte(token), 0600); err != nil {
			t.Fatal(err)
		}
		s, err := NewMatrixServer(c, b)
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
		if g := s.routes["!room"]; g.target != b.Routes[0].Target || len(g.operations) != 1 {
			t.Fatal("wrong route")
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
	if s, err := NewMatrixServer(c, b); err == nil {
		s.Close()
		t.Fatal("public token accepted")
	}
}
