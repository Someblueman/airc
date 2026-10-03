package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Someblueman/airc/pkg/irc"
)

func TestFileSnippetPreservesContentInReceiptInboxAndAudit(t *testing.T) {
	agentEnv(t)
	address := cliTestServer(t)
	path := filepath.Join(t.TempDir(), "example.go")
	source := "func demo() {\n\tvalue := `@fake **literal**`\n\tprintln(value)  // two spaces\n}\n\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	out := mustCLI(t, address, "send", "--nick", "planner", "--to", "muse", "--file", path, "--message", "@muse please review", "--check", "--json")
	var receipt irc.MessageEvent
	if err := json.Unmarshal([]byte(strings.Split(out, "\n")[0]), &receipt); err != nil {
		t.Fatal(err)
	}
	want := "@muse please review\nexample.go\n```go\n" + source + "```"
	if receipt.Message != want {
		t.Fatalf("receipt changed snippet whitespace: %q", receipt.Message)
	}
	check := checkBodies(t, mustCLI(t, address, "check", "--nick", "muse", "--json"))
	if len(check) != 1 || check[0] != want {
		t.Fatalf("inbox changed snippet: %+v", check)
	}
	if got := mustCLI(t, address, "check", "--nick", "fake", "--mentions", "--json"); len(checkBodies(t, got)) != 0 {
		t.Fatalf("code annotation created a mention: %s", got)
	}
	var audit irc.HistoryEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(mustCLI(t, address, "history", irc.AllDirectMessages, "--json"))), &audit); err != nil {
		t.Fatal(err)
	}
	if audit.ID != receipt.ID || audit.Message != want {
		t.Fatalf("audit changed snippet: %+v", audit)
	}
}

func TestSnippetStdinFencesAndValidation(t *testing.T) {
	got, err := snippetMessage("-", "markdown", "Example", strings.NewReader("```go\n  example()\n```\n"))
	if err != nil || got != "Example\n````markdown\n```go\n  example()\n```\n````" {
		t.Fatalf("nested fences = %q, %v", got, err)
	}
	got, err = snippetMessage("", "go", "-", strings.NewReader("\tprintln(1)\r\n"))
	if err != nil || got != "```go\n\tprintln(1)\n```" {
		t.Fatalf("stdin snippet = %q, %v", got, err)
	}
	if _, err := snippetMessage("-", "text", "", strings.NewReader(strings.Repeat("x\r\n", 3000))); err == nil {
		t.Fatal("an oversized CRLF stream was silently truncated")
	}
	for _, bad := range []struct{ path, language, source string }{
		{"", "go", ""}, {"", "go", "\x00"}, {"", "go", "\xff"},
		{"", "go\n@fake", "x"}, {"", "go", strings.Repeat("x", 4096)}, {"-", "go", "-"},
	} {
		if _, err := snippetMessage(bad.path, bad.language, bad.source, strings.NewReader("x")); err == nil {
			t.Errorf("accepted invalid snippet: %+v", bad)
		}
	}
}

func TestCodeRenderingKeepsOperatorsAndSpacingLiteral(t *testing.T) {
	body := "```go\n  x  := `@fake **literal**`\n    \n  # not a heading\n```\n**Review** @real"
	for _, color := range []bool{false, true} {
		r := newRenderer(color, 100, false)
		got := ansiPattern.ReplaceAllString(strings.Join(r.bodyLines(body, 80), "\n"), "")
		if !strings.Contains(got, "  x  := `@fake **literal**`\n    \n  # not a heading") {
			t.Fatalf("code was reformatted (color=%v): %q", color, got)
		}
		if color && !strings.Contains(got, "Review @real") {
			t.Fatalf("normal Markdown stopped working: %q", got)
		}
	}
	line := "    spaced   value := `literal`  "
	if got := strings.Join(wrapCode(line, 8), ""); got != line {
		t.Fatalf("narrow code wrapping lost characters: %q", got)
	}
}
