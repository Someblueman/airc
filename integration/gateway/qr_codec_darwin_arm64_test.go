//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"image"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func qrPayload(length int, seed byte) []byte {
	payload := make([]byte, length)
	for i := range payload {
		payload[i] = byte(i*131 + int(seed))
	}
	if length > 2 {
		payload[0], payload[1], payload[2] = 0, 0xff, seed
	}
	return payload
}

func assertQRImagesBounded(t *testing.T, images []string, want int) {
	t.Helper()
	if len(images) != want || len(images) == 0 || len(images) > 128 {
		t.Fatalf("QR image count=%d want=%d", len(images), want)
	}
	directory, err := os.Lstat(filepath.Dir(images[0]))
	if err != nil || !directory.IsDir() || directory.Mode().Perm() != 0700 {
		t.Fatalf("encoded QR directory is not private: info=%v err=%v", directory, err)
	}
	for _, path := range images {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > 4<<20 {
			t.Fatalf("encoded QR PNG is not a bounded regular file: path=%s info=%v err=%v", path, info, err)
		}
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		config, format, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil || format != "png" || config.Width <= 0 || config.Height <= 0 || config.Width > 2048 || config.Height > 2048 {
			t.Fatalf("encoded image violates PNG/dimension bounds: path=%s config=%+v format=%q decode=%v close=%v", path, config, format, decodeErr, closeErr)
		}
	}
}

func assertQRDecodeRefused(t *testing.T, tool, name string, images []string) {
	t.Helper()
	output := qrFreshPath(t, name)
	if err := qrDecode(t, tool, output, images); !qrCommandExitedWith(err, 1) {
		t.Fatalf("decoder did not refuse invalid sequence %q with a normal validation error: %v", name, err)
	}
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("decoder created output for rejected sequence %q: lstat err=%v", name, err)
	}
}

func qrCommandExitedWith(err error, code int) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ExitCode() == code
}

func TestQRCodecReorderedBinaryRoundTripAndSequenceRefusals(t *testing.T) {
	tool := qrTool(t)
	payload := qrPayload(4*512+73, 0x5a)
	input := qrWritePayload(t, "source.bin", payload)
	images := qrEncode(t, tool, input, qrFreshPath(t, "encoded"))
	assertQRImagesBounded(t, images, 5)
	if source, err := os.ReadFile(input); err != nil || !bytes.Equal(source, payload) {
		t.Fatalf("encoder modified its source: bytes=%d err=%v", len(source), err)
	}
	for left, right := 0, len(images)-1; left < right; left, right = left+1, right-1 {
		images[left], images[right] = images[right], images[left]
	}
	output := qrFreshPath(t, "decoded.bin")
	if err := qrDecode(t, tool, output, images); err != nil {
		t.Fatalf("decode reordered binary payload: %v", err)
	}
	decoded, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("QR round trip changed bytes: got=%d want=%d err=%v", len(decoded), len(payload), err)
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("decoded payload is not private: info=%v err=%v", info, err)
	}

	assertQRDecodeRefused(t, tool, "missing.bin", images[:len(images)-1])
	duplicate := append(append([]string(nil), images...), images[0])
	assertQRDecodeRefused(t, tool, "duplicate.bin", duplicate)

	otherInput := qrWritePayload(t, "other.bin", qrPayload(len(payload), 0xa7))
	otherImages := qrEncode(t, tool, otherInput, qrFreshPath(t, "other-encoded"))
	mixed := append([]string(nil), images...)
	mixed[1] = otherImages[1]
	assertQRDecodeRefused(t, tool, "mixed.bin", mixed)

	corrupt := qrFreshPath(t, "corrupt.png")
	corruptBytes, err := os.ReadFile(images[0])
	if err != nil {
		t.Fatal(err)
	}
	corruptBytes[0] ^= 0xff // Invalid PNG signature cannot be repaired by QR error correction.
	if err := os.WriteFile(corrupt, corruptBytes, 0600); err != nil {
		t.Fatal(err)
	}
	corruptSet := append([]string(nil), images...)
	corruptSet[0] = corrupt
	assertQRDecodeRefused(t, tool, "corrupt.bin", corruptSet)

	wrongDigest := sha256.Sum256([]byte("right"))
	text := "AIRCQR1:" + hex.EncodeToString(wrongDigest[:]) + ":1:0:5:" + base64.StdEncoding.EncodeToString([]byte("wrong"))
	badChunkImage := qrFreshPath(t, "bad-chunk.png")
	qrRenderText(t, text, badChunkImage)
	badOutput := qrFreshPath(t, "bad-digest.bin")
	if err := qrDecode(t, tool, badOutput, []string{badChunkImage}); !qrCommandExitedWith(err, 1) || !strings.Contains(err.Error(), "QR sequence integrity check failed") {
		t.Fatal("independently rendered QR did not reach the complete-digest check", err)
	}
	if _, err := os.Lstat(badOutput); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("digest refusal published output", err)
	}
}

func TestQRCodecPayloadAndFilesystemBoundaries(t *testing.T) {
	tool := qrTool(t)
	payload := qrPayload(65536, 0x11)
	input := qrWritePayload(t, "max.bin", payload)
	images := qrEncode(t, tool, input, qrFreshPath(t, "max-encoded"))
	assertQRImagesBounded(t, images, 128)
	output := qrFreshPath(t, "max-decoded.bin")
	if err := qrDecode(t, tool, output, images); err != nil {
		t.Fatalf("decode maximum payload: %v", err)
	}
	decoded, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(decoded, payload) {
		t.Fatalf("maximum QR round trip changed bytes: got=%d want=%d err=%v", len(decoded), len(payload), err)
	}
	if info, err := os.Stat(output); err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("decoded maximum payload is not private: info=%v err=%v", info, err)
	}

	overLimit := qrWritePayload(t, "over-limit.bin", qrPayload(65537, 0x22))
	if err := runQRCommand(t, 40*time.Second, tool, "encode", "--input", overLimit, "--output", qrFreshPath(t, "over-limit-encoded")); !qrCommandExitedWith(err, 1) {
		t.Fatal("encoder accepted payload above 65536 bytes")
	}

	oneImage := qrEncode(t, tool, qrWritePayload(t, "small.bin", []byte{0, 1, 2, 0xff}), qrFreshPath(t, "small-encoded"))[0]
	qrAssertImageAndPublicationBoundaries(t, tool, oneImage)
	invalidDir := qrFreshPath(t, "directory-input")
	if err := os.Mkdir(invalidDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := runQRCommand(t, 40*time.Second, tool, "encode", "--input", invalidDir, "--output", qrFreshPath(t, "directory-output")); !qrCommandExitedWith(err, 1) {
		t.Fatal("encoder accepted a directory as input")
	}
	linkedInput := qrFreshPath(t, "linked-source.bin")
	if err := os.Symlink(input, linkedInput); err != nil {
		t.Fatal(err)
	}
	if err := runQRCommand(t, 40*time.Second, tool, "encode", "--input", linkedInput, "--output", qrFreshPath(t, "linked-source-output")); !qrCommandExitedWith(err, 1) {
		t.Fatal("encoder followed a final-component symlink")
	}

	nonPNG := qrFreshPath(t, "not-a-png.png")
	if err := os.WriteFile(nonPNG, []byte("not png"), 0600); err != nil {
		t.Fatal(err)
	}
	assertQRDecodeRefused(t, tool, "non-png-output.bin", []string{nonPNG})

	symlink := qrFreshPath(t, "linked.png")
	if err := os.Symlink(oneImage, symlink); err != nil {
		t.Fatal(err)
	}
	assertQRDecodeRefused(t, tool, "symlink-output.bin", []string{symlink})

	fifo := qrFreshPath(t, "input.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if err := runQRCommand(t, 40*time.Second, tool, "decode", "--output", qrFreshPath(t, "fifo-output.bin"), fifo); !qrCommandExitedWith(err, 1) {
		t.Fatal("decoder accepted a FIFO as image input")
	}

	encodeOutput := qrFreshPath(t, "existing-encoded")
	if err := os.Mkdir(encodeOutput, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(encodeOutput, "sentinel")
	if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := runQRCommand(t, 40*time.Second, tool, "encode", "--input", input, "--output", encodeOutput); !qrCommandExitedWith(err, 1) {
		t.Fatal("encoder replaced an existing output directory")
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve" {
		t.Fatalf("encoder modified existing output: got=%q err=%v", got, err)
	}

	decodeOutput := qrFreshPath(t, "existing-decoded.bin")
	if err := os.WriteFile(decodeOutput, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := qrDecode(t, tool, decodeOutput, []string{oneImage}); !qrCommandExitedWith(err, 1) {
		t.Fatal("decoder replaced an existing output file")
	}
	if got, err := os.ReadFile(decodeOutput); err != nil || string(got) != "preserve" {
		t.Fatalf("decoder modified existing output: got=%q err=%v", got, err)
	}
	decodeTarget := qrFreshPath(t, "symlink-target.bin")
	if err := os.WriteFile(decodeTarget, []byte("preserve-link-target"), 0600); err != nil {
		t.Fatal(err)
	}
	decodeLink := qrFreshPath(t, "symlink-output.bin")
	if err := os.Symlink(decodeTarget, decodeLink); err != nil {
		t.Fatal(err)
	}
	if err := qrDecode(t, tool, decodeLink, []string{oneImage}); !qrCommandExitedWith(err, 1) {
		t.Fatal("decoder replaced a final-component output symlink")
	}
	if info, err := os.Lstat(decodeLink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("decoder changed existing output symlink: info=%v err=%v", info, err)
	}
	if got, err := os.ReadFile(decodeTarget); err != nil || string(got) != "preserve-link-target" {
		t.Fatalf("decoder followed output symlink: got=%q err=%v", got, err)
	}
}
