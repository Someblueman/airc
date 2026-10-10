// Package natswire implements bounded NATS client framing without session policy.
package natswire

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const (
	MaxPayload = 32768
	MaxLine    = 4096
	MaxSubject = 255
	MaxSID     = 64
)

var ErrFrame = errors.New("invalid or oversized NATS frame")

type Frame struct {
	Command             string
	Subject, Reply, SID string
	Payload, JSON       []byte
	Error               string
	MaxMessages         *uint32
}

// Subject accepts literal ASCII subject tokens, excluding wildcard syntax.
func Subject(s string) bool {
	if len(s) == 0 || len(s) > MaxSubject {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" {
			return false
		}
	}
	for i := range len(s) {
		if s[i] <= 32 || s[i] >= 127 || s[i] == '*' || s[i] == '>' {
			return false
		}
	}
	return true
}

func sid(s string) bool {
	if len(s) == 0 || len(s) > MaxSID {
		return false
	}
	for i := range len(s) {
		if s[i] <= 32 || s[i] >= 127 {
			return false
		}
	}
	return true
}

func decimal(s string, limit uint64) (uint64, error) {
	if len(s) == 0 || len(s) > 10 {
		return 0, ErrFrame
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrFrame
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n > limit {
		return 0, ErrFrame
	}
	return n, nil
}

func object(b []byte) bool {
	t := bytes.TrimSpace(b)
	return len(t) > 0 && t[0] == '{' && jsoninput.Validate(b, MaxLine) == nil
}

func line(r *bufio.Reader) (string, error) {
	b := make([]byte, 0, 128)
	for {
		c, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if c == '\r' {
			next, err := r.ReadByte()
			if err != nil {
				return "", err
			}
			if next != '\n' || !utf8.Valid(b) {
				return "", ErrFrame
			}
			return string(b), nil
		}
		if c == '\n' || c == 0 || c < 32 && c != '\t' || len(b) == MaxLine {
			return "", ErrFrame
		}
		b = append(b, c)
	}
}

// Read consumes one bounded frame and retains subsequent buffered bytes.
// The caller owns deadlines and must close the connection after framing errors.
func Read(r *bufio.Reader) (Frame, error) {
	s, err := line(r)
	if err != nil {
		return Frame{}, err
	}
	command, args, _ := strings.Cut(s, " ")
	// NATS permits tabs as command/argument separators as well as spaces.
	if tab := strings.IndexByte(command, '\t'); tab >= 0 {
		command, args = command[:tab], s[tab+1:]
	}
	f := Frame{Command: strings.ToUpper(command)}
	fields := strings.FieldsFunc(args, func(c rune) bool { return c == ' ' || c == '\t' })
	var size uint64
	switch f.Command {
	case "INFO", "CONNECT":
		f.JSON = []byte(args)
		if !object(f.JSON) {
			return Frame{}, ErrFrame
		}
	case "PING", "PONG", "+OK":
		if len(fields) != 0 {
			return Frame{}, ErrFrame
		}
	case "-ERR":
		args = strings.Trim(args, " \t")
		if len(args) < 2 || args[0] != '\'' || args[len(args)-1] != '\'' || strings.ContainsAny(args[1:len(args)-1], "'\t") {
			return Frame{}, ErrFrame
		}
		f.Error = args[1 : len(args)-1]
	case "SUB":
		if len(fields) != 2 {
			return Frame{}, ErrFrame
		}
		f.Subject, f.SID = fields[0], fields[1]
	case "UNSUB":
		if len(fields) < 1 || len(fields) > 2 {
			return Frame{}, ErrFrame
		}
		f.SID = fields[0]
		if len(fields) == 2 {
			n, err := decimal(fields[1], 1<<32-1)
			if err != nil {
				return Frame{}, err
			}
			value := uint32(n)
			f.MaxMessages = &value
		}
	case "PUB", "MSG":
		base := 2
		if f.Command == "MSG" {
			base = 3
		}
		if len(fields) != base && len(fields) != base+1 {
			return Frame{}, ErrFrame
		}
		f.Subject = fields[0]
		if f.Command == "MSG" {
			f.SID = fields[1]
		}
		if len(fields) == base+1 {
			f.Reply = fields[base-1]
		}
		size, err = decimal(fields[len(fields)-1], MaxPayload)
		if err != nil {
			return Frame{}, err
		}
	default:
		return Frame{}, ErrFrame
	}
	if (f.Command == "SUB" || f.Command == "PUB" || f.Command == "MSG") && !Subject(f.Subject) || f.Reply != "" && !Subject(f.Reply) || (f.Command == "SUB" || f.Command == "UNSUB" || f.Command == "MSG") && !sid(f.SID) {
		return Frame{}, ErrFrame
	}
	if f.Command == "PUB" || f.Command == "MSG" {
		f.Payload = make([]byte, int(size))
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			clear(f.Payload)
			return Frame{}, err
		}
		var end [2]byte
		if _, err := io.ReadFull(r, end[:]); err != nil {
			clear(f.Payload)
			return Frame{}, err
		}
		if end != [2]byte{'\r', '\n'} {
			clear(f.Payload)
			return Frame{}, ErrFrame
		}
	}
	return f, nil
}
