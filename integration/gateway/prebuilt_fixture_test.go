package gateway_test

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// Supplied artifacts are copied and verified before any fixture process starts.
// Their source/archive provenance must be recorded by the platform build run.
func prebuiltFixtureBinary(pathKey, digestKey, program string, requireRace bool) (root, binary, digest string, err error) {
	source, hasPath := os.LookupEnv(pathKey)
	expected, hasDigest := os.LookupEnv(digestKey)
	if !hasPath && !hasDigest {
		return "", "", "", nil
	}
	decoded, decodeErr := hex.DecodeString(expected)
	if !hasPath || !hasDigest || !filepath.IsAbs(source) || decodeErr != nil || len(decoded) != sha256.Size {
		return "", "", "", fmt.Errorf("%s and %s require an absolute binary path and SHA256", pathKey, digestKey)
	}
	input, err := os.Open(source)
	if err != nil {
		return "", "", "", err
	}
	defer input.Close()
	stat, err := input.Stat()
	if err != nil {
		return "", "", "", err
	}
	if !stat.Mode().IsRegular() || stat.Size() > 128<<20 {
		return "", "", "", errors.New("prebuilt fixture requires a regular file up to 128 MiB")
	}
	root, err = os.MkdirTemp("/tmp", "agw-prebuilt-")
	if err != nil {
		return "", "", "", err
	}
	complete := false
	ownedRoot := root
	defer func() {
		if !complete {
			_ = os.RemoveAll(ownedRoot)
		}
	}()
	binary = filepath.Join(root, "fixture")
	output, err := os.OpenFile(binary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", "", "", err
	}
	hash := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(output, hash), io.LimitReader(input, (128<<20)+1))
	if err := errors.Join(copyErr, output.Close()); err != nil {
		return "", "", "", fmt.Errorf("copy prebuilt fixture: %w", err)
	}
	if n > 128<<20 {
		return "", "", "", errors.New("prebuilt fixture exceeds 128 MiB")
	}
	digest = hex.EncodeToString(hash.Sum(nil))
	if digest != hex.EncodeToString(decoded) {
		return "", "", "", fmt.Errorf("prebuilt fixture SHA256 mismatch: got %s", digest)
	}
	info, err := buildinfo.ReadFile(binary)
	if err != nil {
		return "", "", "", fmt.Errorf("prebuilt fixture Go build information: %w", err)
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if info.Path != program || settings["GOOS"] != runtime.GOOS || settings["GOARCH"] != runtime.GOARCH || (requireRace && settings["-race"] != "true") {
		return "", "", "", errors.New("prebuilt fixture program, platform or race instrumentation mismatch")
	}
	if err := os.Chmod(binary, 0500); err != nil {
		return "", "", "", err
	}
	complete = true
	return root, binary, digest, nil
}
