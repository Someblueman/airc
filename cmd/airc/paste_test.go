package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/protocol"
	"github.com/Someblueman/airc/internal/server"
	"github.com/Someblueman/airc/pkg/irc"
)

// pasteCLIServer starts a daemon that keeps pastes under a state directory.
func pasteCLIServer(t *testing.T) string {
	t.Helper()
	agentEnv(t)
	dir := t.TempDir()
	return cliTestServerSetup(t, server.Config{HistoryLimit: 32}, func(s *server.Server) error {
		return s.RestorePastes(filepath.Join(dir, "pastes.json"))
	})
}

func testPasteFile(t *testing.T, name string, size int) (string, []byte) {
	t.Helper()
	var b bytes.Buffer
	for i := 0; b.Len() < size; i++ {
		b.WriteString("--- line of test output, with some detail about failure ")
		b.WriteString(strings.Repeat("x", i%7))
		b.WriteByte('\n')
	}
	content := b.Bytes()[:size]
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

// send --paste uploads the file and posts a message that shares it: the
// receipt, check rows and paste info describe it, paste get returns the
// bytes, the same file shared again is the same paste, and list shows the
// author's own.
func TestSendPasteRoundTrip(t *testing.T) {
	address := pasteCLIServer(t)
	path, content := testPasteFile(t, "run.log", 20<<10)
	receipt := cliJSON[irc.MessageEvent](t, address, "send", "--nick", "planner", "--channel", "room", "--paste", path, "--message", "tests fail on main")
	id := protocol.PasteID(content)
	if receipt.Paste == nil || receipt.Paste.ID != id || receipt.Paste.Name != "run.log" || receipt.Paste.Size != len(content) || receipt.Paste.Language != "text" {
		t.Fatalf("receipt %+v", receipt)
	}
	want := "tests fail on main\n" + protocol.PasteReferenceLine(*receipt.Paste)
	if receipt.Message != want {
		t.Fatalf("body %q", receipt.Message)
	}
	rows := checkMessages(t, mustCLI(t, address, "check", "--nick", "reviewer", "--channel", "room", "--json"))
	if len(rows) != 1 || rows[0].Paste == nil || rows[0].Paste.ID != id || rows[0].Message != want {
		t.Fatalf("check rows %+v", rows)
	}
	info := cliJSON[pasteRow](t, address, "paste", "info", id)
	if info.Type != "paste" || info.ID != id || info.Name != "run.log" || info.Lines != protocol.PasteLines(content) || info.From != "planner" || info.Created.IsZero() {
		t.Fatalf("info %+v", info)
	}
	if got := mustCLI(t, address, "paste", "get", id); got != string(content) {
		t.Fatalf("paste get returned %d bytes, want %d", len(got), len(content))
	}
	out := filepath.Join(t.TempDir(), "copy.log")
	if text := mustCLI(t, address, "paste", "get", id, "--out", out); !strings.Contains(text, out) {
		t.Fatalf("paste get --out said %q", text)
	}
	if copied, err := os.ReadFile(out); err != nil || !bytes.Equal(copied, content) {
		t.Fatalf("--out copy differs: %v", err)
	}
	if got := cliJSON[pasteRow](t, address, "paste", "get", id); got.Content != string(content) || got.Size != len(content) {
		t.Fatalf("paste get --json %+v", got)
	}
	again := cliJSON[irc.MessageEvent](t, address, "send", "--nick", "reviewer", "--to", "planner", "--paste", path)
	if again.Paste == nil || again.Paste.ID != id || again.Message != protocol.PasteReferenceLine(*again.Paste) {
		t.Fatalf("second share %+v", again)
	}
	mine := jsonLines[pasteRow](t, mustCLI(t, address, "paste", "list", "--nick", "planner", "--json"))
	if len(mine) != 1 || mine[0].ID != id {
		t.Fatalf("planner's pastes %+v", mine)
	}
	if text := mustCLI(t, address, "paste", "list", "--nick", "nobody"); text != "No pastes.\n" {
		t.Fatalf("nobody's pastes %q", text)
	}
	if text := mustCLI(t, address, "paste", "info", id); !strings.Contains(text, "paste:"+id+" run.log (20 KB, ") || !strings.Contains(text, "by planner") {
		t.Fatalf("info text %q", text)
	}
	if _, _, err := cli(t, address, "paste", "get", strings.Repeat("0", 32)); err == nil || !strings.Contains(err.Error(), "not retained") {
		t.Fatalf("unknown paste: %v", err)
	}
}

// --paste - reads stdin under --name, put uploads without posting, and
// binary or empty input is refused before anything is uploaded.
func TestPasteStdinPutAndRefusals(t *testing.T) {
	address := pasteCLIServer(t)
	var stdout, stderr bytes.Buffer
	content := strings.Repeat("diff --git a/x b/x\n+added\n", 400)
	if err := run([]string{"send", "--nick", "planner", "--channel", "room", "--paste", "-", "--name", "fix.diff", "--json", "--addr", address}, strings.NewReader(content), &stdout, &stderr); err != nil {
		t.Fatalf("send --paste -: %v %s", err, stderr.String())
	}
	var receipt irc.MessageEvent
	if err := jsonUnmarshalFirstLine(stdout.String(), &receipt); err != nil || receipt.Paste == nil || receipt.Paste.Name != "fix.diff" || receipt.Paste.Language != "text" {
		t.Fatalf("stdin paste %+v %v", receipt, err)
	}
	stdout.Reset()
	if err := run([]string{"paste", "put", "-", "--nick", "planner", "--name", "notes.md", "--language", "markdown", "--json", "--addr", address}, strings.NewReader("# notes\n"), &stdout, &stderr); err != nil {
		t.Fatalf("paste put: %v %s", err, stderr.String())
	}
	var row pasteRow
	if err := jsonUnmarshalFirstLine(stdout.String(), &row); err != nil || row.ID != protocol.PasteID([]byte("# notes\n")) || row.Language != "markdown" || row.Lines != 1 {
		t.Fatalf("put row %+v %v", row, err)
	}
	if rows := checkMessages(t, mustCLI(t, address, "check", "--nick", "reviewer", "--channel", "room", "--json")); len(rows) != 1 {
		t.Fatalf("put posted a message: %+v", rows)
	}
	binary := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(binary, []byte("\x89PNG\x00\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cli(t, address, "send", "--nick", "planner", "--channel", "room", "--paste", binary); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("binary paste: %v", err)
	}
	for _, args := range [][]string{
		{"send", "--nick", "planner", "--channel", "room", "--paste", binary, "--file", binary},
		{"send", "--nick", "planner", "--channel", "room", "--message", "x", "--name", "n"},
		{"send", "--nick", "planner", "--channel", "room", "--paste", binary, "--message", "-"},
		{"paste", "info", "short"},
		{"paste", "frobnicate"},
	} {
		if _, _, err := cli(t, address, args...); err == nil {
			t.Errorf("airc %s was accepted", strings.Join(args, " "))
		}
	}
}

// A --file snippet that would not fit a message is shared as a paste, with
// a note on stderr; one that fits stays a fenced snippet.
func TestFileSnippetFallsBackToPaste(t *testing.T) {
	address := pasteCLIServer(t)
	path, content := testPasteFile(t, "big.go", 6000)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"send", "--nick", "planner", "--channel", "room", "--file", path, "--message", "see the loop", "--json", "--addr", address}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("send --file: %v %s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "big.go does not fit") || !strings.Contains(stderr.String(), "as a paste") {
		t.Fatalf("stderr %q", stderr.String())
	}
	var receipt irc.MessageEvent
	if err := jsonUnmarshalFirstLine(stdout.String(), &receipt); err != nil || receipt.Paste == nil || receipt.Paste.ID != protocol.PasteID(content) || receipt.Paste.Language != "go" {
		t.Fatalf("fallback receipt %+v %v", receipt, err)
	}
	if !strings.HasPrefix(receipt.Message, "see the loop\npaste:") {
		t.Fatalf("fallback body %q", receipt.Message)
	}
	small, _ := testPasteFile(t, "small.go", 300)
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"send", "--nick", "planner", "--channel", "room", "--file", small, "--json", "--addr", address}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	var snippet irc.MessageEvent
	if err := jsonUnmarshalFirstLine(stdout.String(), &snippet); err != nil || snippet.Paste != nil || !strings.Contains(snippet.Message, "```go\n") || stderr.Len() != 0 {
		t.Fatalf("small snippet %+v %v %q", snippet, err, stderr.String())
	}
	// From stdin too, where the input can be read only once; other content,
	// since the same text would be the paste already stored under big.go.
	piped := append([]byte("piped\n"), content...)
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"send", "--nick", "planner", "--channel", "room", "--file", "-", "--json", "--addr", address}, bytes.NewReader(piped), &stdout, &stderr); err != nil {
		t.Fatalf("send --file -: %v %s", err, stderr.String())
	}
	var fromStdin irc.MessageEvent
	if err := jsonUnmarshalFirstLine(stdout.String(), &fromStdin); err != nil || fromStdin.Paste == nil || fromStdin.Paste.Name != "stdin.txt" || fromStdin.Paste.ID != protocol.PasteID(piped) {
		t.Fatalf("stdin fallback %+v %v", fromStdin, err)
	}
}

// jsonUnmarshalFirstLine decodes the first line of output into value.
func jsonUnmarshalFirstLine(output string, value any) error {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return json.Unmarshal([]byte(line), value)
}
