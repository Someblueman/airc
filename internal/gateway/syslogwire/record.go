package syslogwire

import (
	"bytes"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"
)

// Record owns its bytes. Header fields and structured data are untrusted
// metadata; neither HOSTNAME nor MSGID establishes identity or replay authority.
type Record struct {
	Priority                                    int
	Timestamp, Hostname, AppName, ProcID, MsgID string
	Raw, StructuredData, Message                []byte
}

var timestampSyntax = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?(Z|[+-][0-9]{2}:[0-9]{2})$`)

// Parse accepts version 1 records only. Binary MSG is retained without decoding;
// BOM-marked MSG and structured parameter values must be valid shortest UTF-8.
func Parse(input []byte) (Record, error) {
	if len(input) == 0 || len(input) > MaxRecord {
		return Record{}, ErrRecord
	}
	raw := bytes.Clone(input)
	end := bytes.IndexByte(raw, '>')
	if len(raw) < 4 || raw[0] != '<' || end < 2 || end > 4 {
		return Record{}, ErrRecord
	}
	pri := raw[1:end]
	if len(pri) > 1 && pri[0] == '0' {
		return Record{}, ErrRecord
	}
	for _, b := range pri {
		if b < '0' || b > '9' {
			return Record{}, ErrRecord
		}
	}
	priority, err := strconv.Atoi(string(pri))
	if err != nil || priority > 191 {
		return Record{}, ErrRecord
	}
	rest := raw[end+1:]
	fields := make([]string, 6)
	for i := range fields {
		split := bytes.IndexByte(rest, ' ')
		if split < 1 {
			return Record{}, ErrRecord
		}
		fields[i] = string(rest[:split])
		rest = rest[split+1:]
	}
	if fields[0] != "1" || !timestampOK(fields[1]) {
		return Record{}, ErrRecord
	}
	for i, limit := range []int{255, 48, 128, 32} {
		if !printable(fields[i+2], limit) {
			return Record{}, ErrRecord
		}
	}
	sdLength, err := structuredLength(rest)
	if err != nil {
		return Record{}, err
	}
	var msg []byte
	if sdLength < len(rest) {
		if rest[sdLength] != ' ' {
			return Record{}, ErrRecord
		}
		msg = rest[sdLength+1:]
		if bytes.HasPrefix(msg, []byte{0xef, 0xbb, 0xbf}) && !utf8.Valid(msg[3:]) {
			return Record{}, ErrRecord
		}
	}
	return Record{priority, fields[1], fields[2], fields[3], fields[4], fields[5], raw, rest[:sdLength], msg}, nil
}

func printable(s string, limit int) bool {
	if len(s) == 0 || len(s) > limit {
		return false
	}
	for i := range s {
		if s[i] < 33 || s[i] > 126 {
			return false
		}
	}
	return true
}

func timestampOK(s string) bool {
	if s == "-" {
		return true
	}
	if !timestampSyntax.MatchString(s) {
		return false
	}
	// time.Parse permits offset hour 24/minute 60; RFC 5424 does not.
	if s[len(s)-1] != 'Z' {
		h, _ := strconv.Atoi(s[len(s)-5 : len(s)-3])
		m, _ := strconv.Atoi(s[len(s)-2:])
		if h > 23 || m > 59 {
			return false
		}
	}
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}

func sdName(s []byte) bool {
	if len(s) == 0 || len(s) > 32 {
		return false
	}
	for _, b := range s {
		if b < 33 || b > 126 || b == '=' || b == ']' || b == '"' {
			return false
		}
	}
	return true
}

func structuredLength(s []byte) (int, error) {
	if len(s) == 0 {
		return 0, ErrRecord
	}
	if s[0] == '-' {
		return 1, nil
	}
	seen := map[string]bool{}
	p := 0
	for p < len(s) && s[p] == '[' {
		p++
		start := p
		for p < len(s) && s[p] != ' ' && s[p] != ']' {
			p++
		}
		name := s[start:p]
		if !sdName(name) || seen[string(name)] {
			return 0, ErrRecord
		}
		seen[string(name)] = true
		for p < len(s) && s[p] == ' ' {
			p++
			start = p
			for p < len(s) && s[p] != '=' {
				p++
			}
			if !sdName(s[start:p]) || p+1 >= len(s) || s[p+1] != '"' {
				return 0, ErrRecord
			}
			p += 2
			start = p
			for p < len(s) && s[p] != '"' {
				if s[p] == ']' {
					return 0, ErrRecord
				}
				// Unknown escapes retain both bytes, as required by RFC 5424.
				if s[p] == '\\' && p+1 < len(s) && (s[p+1] == '\\' || s[p+1] == '"' || s[p+1] == ']') {
					p++
				}
				p++
			}
			if p >= len(s) || !utf8.Valid(s[start:p]) {
				return 0, ErrRecord
			}
			p++
		}
		if p >= len(s) || s[p] != ']' {
			return 0, ErrRecord
		}
		p++
	}
	if p == 0 {
		return 0, ErrRecord
	}
	return p, nil
}
