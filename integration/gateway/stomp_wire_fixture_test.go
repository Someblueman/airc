//go:build darwin || linux

package gateway_test

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This client owns its wire implementation independently of the gateway codec.
type sqFrame struct {
	command string
	headers map[string]string
	body    []byte
}

func sqWrite(t *testing.T, w io.Writer, command string, headers []string, body []byte) {
	t.Helper()
	var wire bytes.Buffer
	wire.WriteString(command + "\n")
	for _, h := range headers {
		wire.WriteString(h + "\n")
	}
	if len(body) > 0 {
		wire.WriteString("content-length:" + strconv.Itoa(len(body)) + "\n")
	}
	wire.WriteByte('\n')
	wire.Write(body)
	wire.WriteByte(0)
	if _, err := io.Copy(w, &wire); err != nil {
		t.Fatal(err)
	}
}

func sqLine(r *bufio.Reader) (string, error) {
	var line []byte
	for len(line) <= 1024 {
		b, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if b == '\n' {
			return strings.TrimSuffix(string(line), "\r"), nil
		}
		line = append(line, b)
	}
	return "", errors.New("oversize STOMP client line")
}

func sqDecode(r *bufio.Reader) (sqFrame, error) {
	f := sqFrame{headers: make(map[string]string)}
	var err error
	for f.command == "" {
		f.command, err = sqLine(r)
		if err != nil {
			return f, err
		}
	}
	for i := 0; ; i++ {
		line, err := sqLine(r)
		if err != nil {
			return f, err
		}
		if line == "" {
			break
		}
		if i >= 32 {
			return f, errors.New("excess STOMP client headers")
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" {
			return f, errors.New("malformed STOMP client header")
		}
		// Selected response headers are ASCII and need no escapes. Escaped values
		// are decoded independently for ERROR descriptions and future literals.
		value = strings.NewReplacer(`\r`, "\r", `\n`, "\n", `\c`, ":", `\\`, `\`).Replace(value)
		if _, exists := f.headers[key]; !exists {
			f.headers[key] = value
		}
	}
	if size, ok := f.headers["content-length"]; ok {
		n, err := strconv.Atoi(size)
		if err != nil || n < 0 || n > 32768 {
			return f, errors.New("invalid STOMP client length")
		}
		body := make([]byte, n+1)
		if _, err := io.ReadFull(r, body); err != nil {
			return f, err
		}
		if body[n] != 0 {
			return f, errors.New("missing STOMP terminator")
		}
		f.body = body[:n]
	} else {
		for {
			b, err := r.ReadByte()
			if err != nil {
				return f, err
			}
			if b == 0 {
				break
			}
			if len(f.body) >= 32768 {
				return f, errors.New("oversize STOMP client body")
			}
			f.body = append(f.body, b)
		}
	}
	return f, nil
}

func sqClosed(t *testing.T, conn net.Conn, reader *bufio.Reader) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	b, err := reader.ReadByte()
	if !(errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)) {
		t.Fatal("STOMP peer did not close without another byte", b, err)
	}
}
