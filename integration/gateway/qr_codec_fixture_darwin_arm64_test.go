//go:build darwin && arm64

package gateway_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func qrTool(t *testing.T) string {
	t.Helper()
	output := filepath.Join(t.TempDir(), "qr-tool-bin")
	script, err := filepath.Abs(filepath.Join("..", "..", "scripts", "build-qr-tool.sh"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, script, "--output", output, "--sanitize")
	if data, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build native QR tool: %v: %s", err, data)
	}
	tool := filepath.Join(output, "qr-tool")
	if info, err := os.Stat(tool); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		t.Fatalf("QR tool missing or not executable: %s stat=%v info=%v", tool, err, info)
	}
	return tool
}

func runQRCommand(t *testing.T, timeout time.Duration, tool string, args ...string) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, tool, args...)
	command.Env = append(os.Environ(), "ASAN_OPTIONS=halt_on_error=1:abort_on_error=1")
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("qr-tool %s timed out: %w", args[0], ctx.Err())
		}
		return fmt.Errorf("qr-tool %s: %w: stdout=%q stderr=%q", args[0], err, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || stderr.Len() != 0 {
		return fmt.Errorf("qr-tool %s wrote unexpected diagnostics: stdout=%q stderr=%q", args[0], stdout.String(), stderr.String())
	}
	return nil
}

func qrEncode(t *testing.T, tool, input, directory string) []string {
	t.Helper()
	if err := runQRCommand(t, 40*time.Second, tool, "encode", "--input", input, "--output", directory); err != nil {
		t.Fatalf("encode QR sequence: %v", err)
	}
	images, err := filepath.Glob(filepath.Join(directory, "*.png"))
	if err != nil {
		t.Fatalf("list encoded QR images: %v", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read encoded QR directory: %v", err)
	}
	if len(entries) != len(images) {
		t.Fatalf("encoder output contains non-PNG or extra files: entries=%d pngs=%d", len(entries), len(images))
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || filepath.Ext(entry.Name()) != ".png" {
			t.Fatalf("encoder output contains unexpected file %q", entry.Name())
		}
	}
	sort.Strings(images)
	return images
}

func qrDecode(t *testing.T, tool, output string, images []string) error {
	t.Helper()
	args := []string{"decode", "--output", output}
	args = append(args, images...)
	return runQRCommand(t, 40*time.Second, tool, args...)
}

func qrWritePayload(t *testing.T, name string, payload []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func qrFreshPath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join(t.TempDir(), name)
}

func qrRenderText(t *testing.T, text, output string) {
	t.Helper()
	const source = `import Foundation
import CoreImage
import CoreImage.CIFilterBuiltins
import ImageIO

let arguments = CommandLine.arguments
guard arguments.count == 3, let message = arguments[1].data(using: .ascii) else {
    exit(2)
}
let filter = CIFilter.qrCodeGenerator()
filter.message = message
filter.correctionLevel = "M"
guard let code = filter.outputImage else { exit(3) }
let scale = CGFloat(8)
let quiet = CGFloat(32)
let scaled = code.transformed(by: CGAffineTransform(scaleX: scale, y: scale))
let canvas = scaled.extent.insetBy(dx: -quiet, dy: -quiet)
let background = CIImage(color: CIColor.white).cropped(to: canvas)
let composed = scaled.transformed(by: CGAffineTransform(translationX: quiet, y: quiet))
    .composited(over: background).cropped(to: canvas)
let context = CIContext(options: [.useSoftwareRenderer: true])
guard let image = context.createCGImage(composed, from: canvas) else { exit(4) }
let data = NSMutableData()
guard let destination = CGImageDestinationCreateWithData(data, "public.png" as CFString, 1, nil) else { exit(5) }
CGImageDestinationAddImage(destination, image, nil)
guard CGImageDestinationFinalize(destination) else { exit(6) }
do {
    try Data(referencing: data).write(to: URL(fileURLWithPath: arguments[2]), options: .atomic)
} catch {
    exit(7)
}
`
	sourcePath := filepath.Join(t.TempDir(), "qr-render.swift")
	renderer := filepath.Join(t.TempDir(), "qr-renderer")
	if err := os.WriteFile(sourcePath, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	swiftc, err := exec.LookPath("swiftc")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	compile := exec.CommandContext(ctx, swiftc, "-O", "-warnings-as-errors", "-target", "arm64-apple-macos13.0", sourcePath, "-o", renderer)
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("build independent QR test renderer: %v: %s", err, output)
	}
	render := exec.CommandContext(ctx, renderer, text, output)
	if data, err := render.CombinedOutput(); err != nil || len(data) != 0 {
		t.Fatalf("render independent QR frame: %v: %s", err, data)
	}
}
