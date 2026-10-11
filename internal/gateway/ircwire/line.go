// Package ircwire implements bounded IRCv3 client-side line framing. Parsed
// tags are data, not authentication; only a verified server can supply authority.
package ircwire

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/protocol"
)

const MaxTags = 8191
const MaxMessage = 512
const MaxWire = MaxTags + MaxMessage

var ErrLine = errors.New("invalid or oversized IRC line")

// ErrIdle reports that a read deadline expired before any byte of the next line
// arrived. Nothing was consumed, so the Reader remains usable.
var ErrIdle = errors.New("IRC connection idle")

// Reader bounds storage even when the peer never terminates its line. Apart
// from ErrIdle, an error is terminal for the connection; callers must not try
// to resynchronize it.
type Reader struct{ reader *bufio.Reader }

func NewReader(r io.Reader) *Reader {
	if r == nil {
		return &Reader{}
	}
	return &Reader{reader: bufio.NewReaderSize(r, MaxWire+1)}
}

func (r *Reader) Read() (protocol.Command, error) {
	if r == nil || r.reader == nil {
		return protocol.Command{}, ErrLine
	}
	raw, err := r.reader.ReadSlice('\n')
	if err != nil {
		if len(raw) == 0 && errors.Is(err, os.ErrDeadlineExceeded) {
			return protocol.Command{}, ErrIdle
		}
		return protocol.Command{}, err
	}
	return Parse(raw)
}

// Parse keeps IRC tags opaque and case sensitive. Repeated tags use their last
// value, as specified by IRCv3. The body, including whitespace, is never trimmed.
func Parse(raw []byte) (protocol.Command, error) {
	if len(raw) < 3 || len(raw) > MaxWire || !strings.HasSuffix(string(raw), "\r\n") {
		return protocol.Command{}, ErrLine
	}
	line := string(raw[:len(raw)-2])
	if strings.ContainsAny(line, "\x00\r\n") {
		return protocol.Command{}, ErrLine
	}
	var tags map[string]string
	if line[0] == '@' {
		end := strings.IndexByte(line, ' ')
		if end < 2 || end+1 > MaxTags {
			return protocol.Command{}, ErrLine
		}
		tags = make(map[string]string)
		for raw := range strings.SplitSeq(line[1:end], ";") {
			key, value, _ := strings.Cut(raw, "=")
			if key == "" {
				return protocol.Command{}, ErrLine
			}
			if !utf8.ValidString(value) {
				return protocol.Command{}, ErrLine
			}
			tags[key] = unescape(value)
		}
		line = line[end+1:]
	}
	if len(line)+2 > MaxMessage {
		return protocol.Command{}, ErrLine
	}
	c, err := protocol.Parse(line)
	if err != nil || c.Tags != nil || !commandName(c.Name) {
		return protocol.Command{}, ErrLine
	}
	count := len(c.Params)
	if c.HasTrailing {
		count++
	}
	if count > 15 {
		return protocol.Command{}, ErrLine
	}
	c.Tags = tags
	return c, nil
}

func commandName(s string) bool {
	if len(s) == 3 && s[0] >= '0' && s[0] <= '9' && s[1] >= '0' && s[1] <= '9' && s[2] >= '0' && s[2] <= '9' {
		return true
	}
	if len(s) == 0 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

func unescape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\\' {
			i++
			if i == len(s) {
				break
			}
			c = s[i]
			switch c {
			case ':':
				c = ';'
			case 's':
				c = ' '
			case 'r':
				c = '\r'
			case 'n':
				c = '\n'
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Encode emits a complete untagged client command or refuses it; it never
// truncates a command or splits a body into independently admitted fragments.
func Encode(name string, params []string, trailing *string) ([]byte, error) {
	count := len(params)
	if trailing != nil {
		count++
	}
	if count > 15 || len(name) == 0 || len(name) > MaxMessage-2 {
		return nil, ErrLine
	}
	size := len(name) + 2
	for _, p := range params {
		if len(p) == 0 || len(p) > MaxMessage-size-1 {
			return nil, ErrLine
		}
		size += 1 + len(p)
		if p[0] == ':' || strings.ContainsAny(p, " \r\n\x00") {
			return nil, ErrLine
		}
	}
	if trailing != nil {
		if len(*trailing) > MaxMessage-size-2 {
			return nil, ErrLine
		}
		size += 2 + len(*trailing)
		if strings.ContainsAny(*trailing, "\r\n\x00") {
			return nil, ErrLine
		}
	}
	name = strings.ToUpper(name)
	if !commandName(name) {
		return nil, ErrLine
	}
	line := name
	for _, p := range params {
		line += " " + p
	}
	if trailing != nil {
		line += " :" + *trailing
	}
	return []byte(line + "\r\n"), nil
}
