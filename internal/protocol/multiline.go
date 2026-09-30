package protocol

import (
	"encoding/base64"
	"errors"
	"strings"
	"unicode/utf8"
)

// BodyTag carries the full text of a message that contains line breaks. The
// value is unpadded base64url, so it is safe in a tag and cannot inject lines.
// The ordinary trailing parameter then holds a one-line preview, which is all a
// client that does not understand the tag shows.
const BodyTag = "+airc/body"

// PreviewLimit bounds the preview so a tagged line always fits MaxLineLength.
const PreviewLimit = 400

var ErrBadBody = errors.New("message body is not valid text")

// NormalizeNewlines converts CRLF and lone CR to LF.
func NormalizeNewlines(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

func EncodeBody(body string) string { return base64.RawURLEncoding.EncodeToString([]byte(body)) }

// DecodeBody reverses EncodeBody and rejects anything that could not have been a
// message: invalid UTF-8, NUL, or carriage returns.
func DecodeBody(encoded string) (string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || !utf8.Valid(raw) || strings.ContainsAny(string(raw), "\x00\r") {
		return "", ErrBadBody
	}
	return string(raw), nil
}

// Preview flattens a multi-line body into one line for clients that cannot read
// BodyTag. Single-line text is returned unchanged.
func Preview(body string) string {
	if !strings.Contains(body, "\n") {
		return body
	}
	var parts []string
	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			parts = append(parts, line)
		}
	}
	flat := strings.Join(parts, " ⏎ ")
	if len(flat) <= PreviewLimit {
		return flat
	}
	cut := PreviewLimit
	for cut > 0 && !utf8.RuneStart(flat[cut]) {
		cut--
	}
	return flat[:cut] + "…"
}
