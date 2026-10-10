//go:build unix

package auth

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOSCOREContextNativeConfiguration(t *testing.T) {
	binary := os.Getenv("AIRC_OSCORE_CONTEXT_CHECK")
	if binary == "" {
		t.Skip("requires the owned sanitizer-enabled OSCORE native build")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native check path must be absolute")
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	material, err := CreateOSCOREContext(filepath.Join(root, "keys.json"), OSCOREOwner{Realm: "fixture", Principal: "device", Binding: "backend"})
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(root, "server.conf"), filepath.Join(root, "peer.conf")}
	for i, path := range paths {
		data, err := material.NativeConfig(i == 0)
		if err != nil {
			t.Fatal(err)
		}
		err = os.WriteFile(path, data, 0600)
		clear(data)
		if err != nil {
			t.Fatal(err)
		}
	}
	if output, err := exec.CommandContext(t.Context(), binary, paths...).CombinedOutput(); err != nil {
		t.Fatalf("native context check: %v %s", err, output)
	}
}
