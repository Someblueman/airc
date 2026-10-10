package natswire

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// Encode emits canonical framing and applies the same selected-profile bounds
// as Read. Frame fields unrelated to its command are refused, not silently lost.
func Encode(f Frame) ([]byte, error) {
	if len(f.Command) > 7 || len(f.Subject) > MaxSubject || len(f.Reply) > MaxSubject || len(f.SID) > MaxSID || len(f.JSON) > MaxLine || len(f.Error) > MaxLine || len(f.Payload) > MaxPayload {
		return nil, ErrFrame
	}
	f.Command = strings.ToUpper(f.Command)
	var args string
	switch f.Command {
	case "INFO", "CONNECT":
		if !object(f.JSON) {
			return nil, ErrFrame
		}
		args = string(f.JSON)
	case "PING", "PONG", "+OK":
	case "-ERR":
		args = "'" + f.Error + "'"
	case "SUB":
		args = f.Subject + " " + f.SID
	case "UNSUB":
		args = f.SID
		if f.MaxMessages != nil {
			args += " " + strconv.FormatUint(uint64(*f.MaxMessages), 10)
		}
	case "PUB", "MSG":
		if len(f.Payload) > MaxPayload {
			return nil, ErrFrame
		}
		args = f.Subject
		if f.Command == "MSG" {
			args += " " + f.SID
		}
		if f.Reply != "" {
			args += " " + f.Reply
		}
		args += " " + strconv.Itoa(len(f.Payload))
	default:
		return nil, ErrFrame
	}
	line := f.Command
	if args != "" {
		line += " " + args
	}
	if len(line) > MaxLine {
		return nil, ErrFrame
	}
	out := []byte(line + "\r\n")
	if f.Command == "PUB" || f.Command == "MSG" {
		out = append(out, f.Payload...)
		out = append(out, '\r', '\n')
	}
	decoded, err := Read(bufio.NewReader(bytes.NewReader(out)))
	if err != nil || !equal(f, decoded) {
		return nil, ErrFrame
	}
	return out, nil
}

func equal(a, b Frame) bool {
	if a.Command != b.Command || a.Subject != b.Subject || a.Reply != b.Reply || a.SID != b.SID || a.Error != b.Error || !bytes.Equal(a.Payload, b.Payload) || !bytes.Equal(a.JSON, b.JSON) {
		return false
	}
	if a.MaxMessages == nil || b.MaxMessages == nil {
		return a.MaxMessages == nil && b.MaxMessages == nil
	}
	return *a.MaxMessages == *b.MaxMessages
}
