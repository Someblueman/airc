//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func qrAssertImageAndPublicationBoundaries(t *testing.T, tool, staticImage string) {
	t.Helper()
	data, err := os.ReadFile(staticImage)
	if err != nil || len(data) < 33 || string(data[12:16]) != "IHDR" {
		t.Fatal("missing native PNG header", err)
	}
	// A valid single-frame APNG still has ImageIO image count 1. The SDK's
	// container PNG dictionary is needed to distinguish it from a static PNG.
	animation := qrPNGChunk("acTL", binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, 1), 0))
	frame := binary.BigEndian.AppendUint32(nil, 0)
	frame = append(frame, data[16:24]...)
	frame = binary.BigEndian.AppendUint32(frame, 0)
	frame = binary.BigEndian.AppendUint32(frame, 0)
	frame = binary.BigEndian.AppendUint16(frame, 1)
	frame = binary.BigEndian.AppendUint16(frame, 100)
	frame = append(frame, 0, 0)
	animation = append(animation, qrPNGChunk("fcTL", frame)...)
	animated := append(bytes.Clone(data[:33]), animation...)
	animated = append(animated, data[33:]...)
	animatedPath := qrWritePayload(t, "one-frame-apng.png", animated)
	output := qrFreshPath(t, "animated-output.bin")
	if err := qrDecode(t, tool, output, []string{animatedPath}); !qrCommandExitedWith(err, 1) || !strings.Contains(err.Error(), "static PNG") {
		t.Fatal("one-frame APNG did not hit static-image refusal", err)
	}
	qrAssertNoOutput(t, output)

	var oversized bytes.Buffer
	if err := png.Encode(&oversized, image.NewGray(image.Rect(0, 0, 2049, 1))); err != nil {
		t.Fatal(err)
	}
	tooWide := qrWritePayload(t, "too-wide.png", oversized.Bytes())
	output = qrFreshPath(t, "wide-output.bin")
	if err := qrDecode(t, tool, output, []string{tooWide}); !qrCommandExitedWith(err, 1) || !strings.Contains(err.Error(), "single bounded PNG") {
		t.Fatal("oversized dimensions reached QR processing", err)
	}
	qrAssertNoOutput(t, output)

	tooLarge := append(bytes.Clone(data), make([]byte, (4<<20)+1-len(data))...)
	tooLargePath := qrWritePayload(t, "too-large.png", tooLarge)
	output = qrFreshPath(t, "large-output.bin")
	if err := qrDecode(t, tool, output, []string{tooLargePath}); !qrCommandExitedWith(err, 1) || !strings.Contains(err.Error(), "bounded regular file") {
		t.Fatal("oversized PNG file reached image decoding", err)
	}
	qrAssertNoOutput(t, output)

	tooMany := make([]string, 129)
	for i := range tooMany {
		tooMany[i] = staticImage
	}
	assertQRDecodeRefused(t, tool, "too-many.bin", tooMany)

	parent := t.TempDir()
	if err := os.Chmod(parent, 0770); err != nil {
		t.Fatal(err)
	}
	output = filepath.Join(parent, "unsafe-parent.bin")
	if err := qrDecode(t, tool, output, []string{staticImage}); !qrCommandExitedWith(err, 1) || !strings.Contains(err.Error(), "output parent") {
		t.Fatal("decoder published in group-writable parent", err)
	}
	qrAssertNoOutput(t, output)
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 0 {
		t.Fatal("unsafe publication parent gained temporary files", entries, err)
	}
}

func qrPNGChunk(kind string, payload []byte) []byte {
	chunk := binary.BigEndian.AppendUint32(nil, uint32(len(payload)))
	chunk = append(chunk, kind...)
	chunk = append(chunk, payload...)
	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

func qrAssertNoOutput(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusal published output", err)
	}
}
