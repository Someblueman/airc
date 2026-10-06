package gateway_test

import (
	"crypto/sha256"
	"debug/buildinfo"
	"fmt"
	"os"
	"testing"
)

func TestPrebuiltFixtureCopiesVerifiedExecutable(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	info, err := buildinfo.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	expected := fmt.Sprintf("%x", sha256.Sum256(bytes))
	t.Setenv("AGW_TEST_BINARY", source)
	t.Setenv("AGW_TEST_SHA256", expected)
	root, binary, digest, err := prebuiltFixtureBinary("AGW_TEST_BINARY", "AGW_TEST_SHA256", info.Path, gatewayTestRace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	if binary == source || digest != expected {
		t.Fatal("fixture did not copy and verify the executable", binary, digest)
	}
	stat, err := os.Stat(binary)
	if err != nil || stat.Mode().Perm() != 0500 {
		t.Fatal("fixture executable permissions", stat, err)
	}
	for _, tc := range []struct{ name, path, digest, program string }{
		{"missing-path", "", expected, info.Path},
		{"missing-digest", source, "", info.Path},
		{"relative-path", "fixture", expected, info.Path},
		{"invalid-digest", source, "not-sha256", info.Path},
		{"wrong-digest", source, fmt.Sprintf("%064d", 0), info.Path},
		{"wrong-program", source, expected, "other/program"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGW_TEST_BINARY", tc.path)
			t.Setenv("AGW_TEST_SHA256", tc.digest)
			root, binary, _, err := prebuiltFixtureBinary("AGW_TEST_BINARY", "AGW_TEST_SHA256", tc.program, gatewayTestRace)
			if err == nil || root != "" || binary != "" {
				t.Fatal("invalid prebuilt fixture was accepted", root, binary, err)
			}
		})
	}
}
