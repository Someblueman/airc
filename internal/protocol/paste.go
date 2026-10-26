package protocol

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A paste is text larger than a message: a diff, a log, test output. It is
// uploaded in chunks with the paste-begin, paste-chunk and paste-end CHAT
// actions, stored by the daemon under an ID derived from its content, and
// shared by a message that carries the ID in PasteTag; the message's
// metadata then describes the paste and its body ends with
// PasteReferenceLine, so a client that knows no pastes still sees what was
// shared. paste-get reads it back in pages.

// PasteTag on a PRIVMSG or REPLY names the paste the message shares.
const PasteTag = "+airc/paste"

// PasteChunkBytes is the most raw bytes one paste-chunk request or one
// paste-get entry carries: base64 of it fits a CHAT request with room for
// the rest of the JSON, and a 777 line.
const PasteChunkBytes = 4096

// MaxPasteUploads bounds the uploads one connection may have in flight.
const MaxPasteUploads = 4

// MaxPasteNameBytes bounds a paste's name, a file name in practice.
const MaxPasteNameBytes = 120

// PasteInfo describes a paste: its ID (the first 32 hex digits of the
// SHA-256 of its content), name, language, size in bytes and line count.
type PasteInfo struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"`
	Size     int    `json:"size"`
	Lines    int    `json:"lines"`
}

// PasteID is the ID of content: the first 32 hex digits of its SHA-256, so
// the same text uploaded twice is one paste.
func PasteID(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:16])
}

// PasteLines counts the lines of content: one per line feed, plus one for a
// last line without one.
func PasteLines(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	n := strings.Count(string(content), "\n")
	if content[len(content)-1] != '\n' {
		n++
	}
	return n
}

// ValidPasteText reports whether content is text a paste may hold: valid
// UTF-8 with no NUL byte. Anything else is treated as binary and refused.
func ValidPasteText(content []byte) bool {
	return utf8.Valid(content) && !strings.ContainsRune(string(content), 0)
}

// ValidPasteName reports whether name may name a paste: 1 to
// MaxPasteNameBytes bytes of printable text with no path separators and no
// leading or trailing space.
func ValidPasteName(name string) bool {
	if name == "" || len(name) > MaxPasteNameBytes || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.ContainsAny(name, "/\\") {
		return false
	}
	return strings.IndexFunc(name, func(r rune) bool { return !unicode.IsPrint(r) }) < 0
}

// ValidPasteLanguage reports whether language is a short language name such
// as go, python or text: up to 30 characters of letters, digits and _+.-#.
// The empty string is valid and means unknown.
func ValidPasteLanguage(language string) bool {
	if len(language) > 30 {
		return false
	}
	return strings.IndexFunc(language, func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_+.-#", c))
	}) < 0
}

// FormatPasteSize writes a size for a reader: bytes below 1 KB, else KB
// (1024 bytes) with one decimal below 10 KB, then MB.
func FormatPasteSize(size int) string {
	switch {
	case size < 1024:
		return fmt.Sprintf("%d B", size)
	case size < 10*1024:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(size)/1024), ".0") + " KB"
	case size < 1024*1024:
		return fmt.Sprintf("%d KB", (size+512)/1024)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(size)/(1024*1024)), ".0") + " MB"
}

// PasteReferenceLine is the last line of a message that shares a paste,
// readable by a client that knows no pastes: "paste:ID NAME (SIZE, N lines)".
func PasteReferenceLine(p PasteInfo) string {
	lines := "lines"
	if p.Lines == 1 {
		lines = "line"
	}
	name := p.Name
	if name == "" {
		name = "paste"
	}
	return fmt.Sprintf("paste:%s %s (%s, %d %s)", p.ID, name, FormatPasteSize(p.Size), p.Lines, lines)
}

// PasteMessageBody is the body of a message that shares p: the caption, if
// any, then the reference line.
func PasteMessageBody(caption string, p PasteInfo) string {
	caption = strings.TrimRight(NormalizeNewlines(caption), "\n")
	if strings.TrimSpace(caption) == "" {
		return PasteReferenceLine(p)
	}
	return caption + "\n" + PasteReferenceLine(p)
}

// PasteCaption is the body of a message that shares a paste without its
// reference line, for a client that renders the paste itself.
func PasteCaption(body string, p PasteInfo) string {
	return strings.TrimRight(strings.TrimSuffix(body, PasteReferenceLine(p)), "\n")
}
