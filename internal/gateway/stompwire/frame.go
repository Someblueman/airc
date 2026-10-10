// Package stompwire implements bounded STOMP 1.2 framing, without session policy.
package stompwire

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxFrame   = 65536
	MaxBody    = 32768
	MaxLine    = 1024
	MaxHeaders = 32
)

var ErrFrame = errors.New("invalid or oversized STOMP frame")

type Header struct{ Key, Value string }
type Frame struct {
	Command string
	Headers []Header
	Body    []byte
}

// Header returns the first occurrence, as specified by STOMP 1.2.
func (f Frame) Header(key string) (string, bool) {
	for _, h := range f.Headers {
		if h.Key == key {
			return h.Value, true
		}
	}
	return "", false
}

func commandOK(s string) bool {
	switch s {
	case "CONNECT", "STOMP", "CONNECTED", "SEND", "SUBSCRIBE", "UNSUBSCRIBE", "ACK", "NACK", "BEGIN", "COMMIT", "ABORT", "DISCONNECT", "MESSAGE", "RECEIPT", "ERROR":
		return true
	}
	return false
}

func bodyOK(s string) bool  { return s == "SEND" || s == "MESSAGE" || s == "ERROR" }
func escaped(s string) bool { return s != "CONNECT" && s != "CONNECTED" }

func length(s string) (int, error) {
	if s == "" || len(s) > 10 {
		return 0, ErrFrame
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrFrame
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n > MaxBody {
		return 0, ErrFrame
	}
	return int(n), nil
}

func decode(s string, escape bool) (string, error) {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return "", ErrFrame
	}
	if !escape {
		return s, nil
	}
	if strings.ContainsRune(s, ':') {
		return "", ErrFrame
	}
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' {
			out.WriteByte(s[i])
			continue
		}
		i++
		if i == len(s) {
			return "", ErrFrame
		}
		switch s[i] {
		case 'r':
			out.WriteByte('\r')
		case 'n':
			out.WriteByte('\n')
		case 'c':
			out.WriteByte(':')
		case '\\':
			out.WriteByte('\\')
		default:
			return "", ErrFrame
		}
	}
	return out.String(), nil
}

// Read returns an empty command for a single heartbeat EOL. The caller owns
// deadlines; this function never consumes an unbounded run of heartbeats.
func Read(r *bufio.Reader) (Frame, error) {
	var f Frame
	used := 0
	line := func() (string, error) {
		var b []byte
		for {
			c, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			used++
			if used > MaxFrame || (len(b) > MaxLine && !(len(b) == MaxLine+1 && b[len(b)-1] == '\r' && c == '\n')) {
				return "", ErrFrame
			}
			if c == '\n' {
				if len(b) > 0 && b[len(b)-1] == '\r' {
					b = b[:len(b)-1]
				}
				if len(b) > MaxLine || bytes.ContainsAny(b, "\r\x00") || !utf8.Valid(b) {
					return "", ErrFrame
				}
				return string(b), nil
			}
			b = append(b, c)
		}
	}
	var err error
	f.Command, err = line()
	if err != nil {
		return f, err
	}
	if f.Command == "" {
		return f, nil
	}
	if !commandOK(f.Command) {
		return Frame{}, ErrFrame
	}
	for {
		s, err := line()
		if err != nil {
			return Frame{}, err
		}
		if s == "" {
			break
		}
		if len(f.Headers) == MaxHeaders {
			return Frame{}, ErrFrame
		}
		key, value, ok := strings.Cut(s, ":")
		if !ok || key == "" {
			return Frame{}, ErrFrame
		}
		key, err = decode(key, escaped(f.Command))
		if err != nil {
			return Frame{}, err
		}
		value, err = decode(value, escaped(f.Command))
		if err != nil {
			return Frame{}, err
		}
		f.Headers = append(f.Headers, Header{key, value})
	}
	if s, ok := f.Header("content-length"); ok {
		n, err := length(s)
		if err != nil || used+n+1 > MaxFrame || (!bodyOK(f.Command) && n != 0) {
			return Frame{}, ErrFrame
		}
		b := make([]byte, n+1)
		if _, err := io.ReadFull(r, b); err != nil {
			return Frame{}, err
		}
		if b[n] != 0 {
			return Frame{}, ErrFrame
		}
		f.Body = b[:n]
	} else {
		for {
			c, err := r.ReadByte()
			if err != nil {
				return Frame{}, err
			}
			used++
			if used > MaxFrame {
				return Frame{}, ErrFrame
			}
			if c == 0 {
				break
			}
			if !bodyOK(f.Command) || len(f.Body) == MaxBody {
				return Frame{}, ErrFrame
			}
			f.Body = append(f.Body, c)
		}
	}
	return f, nil
}

func encode(s string, escape bool) (string, error) {
	if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
		return "", ErrFrame
	}
	if !escape {
		if strings.ContainsAny(s, "\r\n") {
			return "", ErrFrame
		}
		return s, nil
	}
	return strings.NewReplacer("\\", "\\\\", "\r", "\\r", "\n", "\\n", ":", "\\c").Replace(s), nil
}

func Encode(f Frame) ([]byte, error) {
	if f.Command == "" {
		if len(f.Headers) != 0 || len(f.Body) != 0 {
			return nil, ErrFrame
		}
		return []byte{'\n'}, nil
	}
	if !commandOK(f.Command) || len(f.Headers) > MaxHeaders || len(f.Body) > MaxBody || (!bodyOK(f.Command) && len(f.Body) != 0) {
		return nil, ErrFrame
	}
	if s, ok := f.Header("content-length"); ok {
		n, err := length(s)
		if err != nil || n != len(f.Body) {
			return nil, ErrFrame
		}
	} else if bytes.IndexByte(f.Body, 0) >= 0 {
		return nil, ErrFrame
	}
	var out bytes.Buffer
	out.WriteString(f.Command + "\n")
	for _, h := range f.Headers {
		if len(h.Key)+1+len(h.Value) > MaxLine {
			return nil, ErrFrame
		}
		key, err := encode(h.Key, escaped(f.Command))
		if err != nil || key == "" || (!escaped(f.Command) && strings.Contains(key, ":")) {
			return nil, ErrFrame
		}
		value, err := encode(h.Value, escaped(f.Command))
		if err != nil || len(key)+1+len(value) > MaxLine {
			return nil, ErrFrame
		}
		out.WriteString(key + ":" + value + "\n")
	}
	out.WriteByte('\n')
	out.Write(f.Body)
	out.WriteByte(0)
	if out.Len() > MaxFrame {
		return nil, ErrFrame
	}
	return out.Bytes(), nil
}
