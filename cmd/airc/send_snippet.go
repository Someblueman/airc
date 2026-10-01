package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/pkg/irc"
)

// Snippets are ordinary fenced Markdown messages: older clients preserve the
// text and the existing history/receipt schema needs no migration.
func snippetMessage(path, language, message string, stdin io.Reader) (string, error) {
	caption, source := "", message
	reader := stdin
	read := message == "-"
	if path != "" {
		if message == "-" {
			return "", errors.New("with --file, --message is a caption; use --file - for stdin")
		}
		caption, read = message, true
		if path != "-" {
			info, err := os.Stat(path)
			if err != nil {
				return "", fmt.Errorf("inspect snippet: %w", err)
			}
			if !info.Mode().IsRegular() {
				return "", errors.New("--file must be a regular file; use --file - for stdin")
			}
			file, err := os.Open(path)
			if err != nil {
				return "", fmt.Errorf("open snippet: %w", err)
			}
			defer file.Close()
			reader = file
			name := filepath.Base(path)
			if caption != "" {
				caption += "\n"
			}
			caption += name
			if language == "" {
				language = snippetLanguage(name)
			}
		}
	}
	if read {
		// CRLF normalization can halve the byte count. Read at most twice the
		// message limit plus one lookahead byte, and always reject overflow.
		data, err := io.ReadAll(io.LimitReader(reader, 2*4096+1))
		if err != nil {
			return "", fmt.Errorf("read snippet: %w", err)
		}
		if len(data) > 2*4096 {
			return "", errors.New("snippet input exceeds the message limit; share a smaller excerpt")
		}
		source = string(data)
	}
	source, caption = irc.NormalizeMessage(source), irc.NormalizeMessage(caption)
	if !utf8.ValidString(source) || strings.ContainsRune(source, 0) {
		return "", errors.New("snippet must be valid UTF-8 without NUL")
	}
	if strings.TrimSpace(source) == "" {
		return "", errors.New("snippet is empty")
	}
	if language == "" {
		language = "text"
	}
	if len(language) > 30 || strings.IndexFunc(language, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_+.-#", c))
	}) >= 0 {
		return "", errors.New("--language must be a short language name, such as go, python or text")
	}
	// Choose a fence longer than any backtick run in the source.
	longest, run := 2, 0
	for _, c := range source {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", longest+1)
	body := fence + language + "\n" + source
	if !strings.HasSuffix(source, "\n") {
		body += "\n"
	}
	body += fence
	if caption != "" {
		body = caption + "\n" + body
	}
	if len(body) > 4096 {
		return "", errors.New("formatted snippet exceeds 4096 bytes including its caption and fences; share a smaller excerpt")
	}
	return body, nil
}

func snippetLanguage(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".sh", ".bash":
		return "bash"
	case ".json":
		return "json"
	case ".c", ".h":
		return "c"
	case ".cpp", ".hpp":
		return "cpp"
	case ".sql":
		return "sql"
	default:
		return "text"
	}
}
