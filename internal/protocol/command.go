package protocol

import (
	"errors"
	"strings"
)

const MaxLineLength = 8192

var ErrMalformed = errors.New("malformed IRC line")

// Command is one parsed IRC-style line. Tags and prefixes are optional.
type Command struct {
	Tags     map[string]string
	Prefix   string
	Name     string
	Params   []string
	Trailing string
	// HasTrailing reports whether a trailing parameter was present even if it is
	// empty, which distinguishes "TOPIC #room" (a query) from "TOPIC #room :".
	HasTrailing bool
}

func Parse(line string) (Command, error) {
	var cmd Command
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	if len(line) == 0 || len(line) > MaxLineLength || strings.ContainsAny(line, "\r\n\x00") {
		return cmd, ErrMalformed
	}
	if line[0] == '@' {
		end := strings.IndexByte(line, ' ')
		if end < 2 {
			return cmd, ErrMalformed
		}
		cmd.Tags = make(map[string]string)
		for raw := range strings.SplitSeq(line[1:end], ";") {
			key, value, found := strings.Cut(raw, "=")
			if key == "" {
				return cmd, ErrMalformed
			}
			if found {
				value = unescapeTag(value)
			}
			cmd.Tags[key] = value
		}
		line = strings.TrimLeft(line[end+1:], " ")
	}
	if strings.HasPrefix(line, ":") {
		end := strings.IndexByte(line, ' ')
		if end < 2 {
			return cmd, ErrMalformed
		}
		cmd.Prefix = line[1:end]
		line = strings.TrimLeft(line[end+1:], " ")
	}
	if line == "" {
		return cmd, ErrMalformed
	}
	if end := strings.IndexByte(line, ' '); end >= 0 {
		cmd.Name, line = strings.ToUpper(line[:end]), strings.TrimLeft(line[end+1:], " ")
	} else {
		cmd.Name, line = strings.ToUpper(line), ""
	}
	if cmd.Name == "" {
		return cmd, ErrMalformed
	}
	for line != "" {
		if line[0] == ':' {
			cmd.Trailing = line[1:]
			cmd.HasTrailing = true
			return cmd, nil
		}
		end := strings.IndexByte(line, ' ')
		if end < 0 {
			cmd.Params = append(cmd.Params, line)
			break
		}
		cmd.Params = append(cmd.Params, line[:end])
		line = strings.TrimLeft(line[end+1:], " ")
	}
	return cmd, nil
}

func (c Command) Param(index int) (string, bool) {
	if index < 0 || index >= len(c.Params) {
		return "", false
	}
	return c.Params[index], true
}

func (c Command) Text() string {
	if c.Trailing != "" || len(c.Params) == 0 {
		return c.Trailing
	}
	return c.Params[len(c.Params)-1]
}

func Format(prefix, name string, params []string, trailing string) string {
	var b strings.Builder
	if prefix != "" {
		b.WriteByte(':')
		b.WriteString(prefix)
		b.WriteByte(' ')
	}
	b.WriteString(strings.ToUpper(name))
	for _, param := range params {
		b.WriteByte(' ')
		b.WriteString(param)
	}
	if trailing != "" {
		b.WriteString(" :")
		b.WriteString(trailing)
	}
	out := b.String()
	if len(out) > MaxLineLength {
		return ":server ERROR :line too long"
	}
	return out + "\r\n"
}

// IsChannel reports whether target names a channel rather than a nickname.
func IsChannel(target string) bool {
	return strings.HasPrefix(target, "#") || strings.HasPrefix(target, "&")
}

var tagEscaper = strings.NewReplacer("\\", "\\\\", ";", "\\:", " ", "\\s", "\r", "\\r", "\n", "\\n")

func EscapeTag(value string) string { return tagEscaper.Replace(value) }

func unescapeTag(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 == len(value) {
			b.WriteByte(value[i])
			continue
		}
		i++
		switch value[i] {
		case ':':
			b.WriteByte(';')
		case 's':
			b.WriteByte(' ')
		case '\\':
			b.WriteByte('\\')
		case 'r':
			b.WriteByte('\r')
		case 'n':
			b.WriteByte('\n')
		default:
			b.WriteByte(value[i])
		}
	}
	return b.String()
}
