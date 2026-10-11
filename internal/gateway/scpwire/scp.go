// Package scpwire implements the single-file upload subset of legacy OpenSSH
// SCP used by the gateway's forced command.
package scpwire

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MaxRequestBytes = 16 << 10
	maxHeaderBytes  = 64
	overallTimeout  = 20 * time.Second
	transferTimeout = 10 * time.Second
	writeTimeout    = 3 * time.Second
	admitTimeout    = 4 * time.Second
)

var (
	ErrProtocol  = errors.New("scp upload refused")
	ErrAdmission = errors.New("scp request admission failed")
)

type Stream interface {
	io.ReadWriteCloser
	SetReadDeadline(time.Time) error
	SetWriteDeadline(time.Time) error
}

// Receive accepts exactly one request.json file and returns only after its
// durable admission callback succeeds and the final SCP acknowledgement is
// written. The callback must finish within its context and must not retain the
// body slice; Receive clears it when the callback returns. The caller owns
// normal stream closure.
func Receive(parent context.Context, stream Stream, admit func(context.Context, []byte) error) error {
	if parent == nil || stream == nil || admit == nil {
		return ErrProtocol
	}
	ctx, cancel := context.WithTimeout(parent, overallTimeout)
	defer cancel()
	closed := make(chan struct{})
	stopClose := context.AfterFunc(ctx, func() {
		_ = stream.Close()
		close(closed)
	})
	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			if !stopClose() {
				<-closed
			}
		})
	}
	defer stop()

	if err := writeControl(ctx, stream, []byte{0}); err != nil {
		return ErrProtocol
	}
	readDeadline := time.Now().Add(transferTimeout)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(readDeadline) {
		readDeadline = deadline
	}
	if err := stream.SetReadDeadline(readDeadline); err != nil {
		return refuse(ctx, stream, ErrProtocol)
	}
	header, err := readHeader(stream)
	if err != nil {
		return refuse(ctx, stream, ErrProtocol)
	}
	size, ok := parseHeader(header)
	clear(header)
	if !ok {
		return refuse(ctx, stream, ErrProtocol)
	}
	if err := writeControl(ctx, stream, []byte{0}); err != nil {
		return ErrProtocol
	}
	body := make([]byte, size)
	defer clear(body)
	if _, err := io.ReadFull(stream, body); err != nil {
		return refuse(ctx, stream, ErrProtocol)
	}
	var senderStatus [1]byte
	if _, err := io.ReadFull(stream, senderStatus[:]); err != nil || senderStatus[0] != 0 {
		return refuse(ctx, stream, ErrProtocol)
	}
	if ctx.Err() != nil {
		return ErrProtocol
	}
	admitCtx, admitCancel := context.WithTimeout(ctx, admitTimeout)
	err = admit(admitCtx, body)
	admitCancel()
	if err != nil {
		return errors.Join(refuse(ctx, stream, ErrAdmission), err)
	}
	if ctx.Err() != nil {
		return ErrProtocol
	}
	if err := writeControl(ctx, stream, []byte{0}); err != nil {
		return ErrProtocol
	}
	return nil
}

func readHeader(stream Stream) ([]byte, error) {
	line := make([]byte, 0, maxHeaderBytes)
	var one [1]byte
	for len(line) < maxHeaderBytes {
		if _, err := io.ReadFull(stream, one[:]); err != nil {
			clear(line)
			return nil, err
		}
		line = append(line, one[0])
		if one[0] == '\n' {
			return line, nil
		}
	}
	clear(line)
	return nil, ErrProtocol
}

func parseHeader(line []byte) (int, bool) {
	if len(line) < len("C0600 1 request.json\n") || line[len(line)-1] != '\n' || bytes.IndexByte(line[:len(line)-1], '\r') >= 0 {
		return 0, false
	}
	value := string(line[:len(line)-1])
	if !strings.HasPrefix(value, "C0600 ") {
		return 0, false
	}
	value = strings.TrimPrefix(value, "C0600 ")
	space := strings.IndexByte(value, ' ')
	if space <= 0 || value[space+1:] != "request.json" {
		return 0, false
	}
	sizeText := value[:space]
	if len(sizeText) > 1 && sizeText[0] == '0' {
		return 0, false
	}
	for i := range sizeText {
		if sizeText[i] < '0' || sizeText[i] > '9' {
			return 0, false
		}
	}
	size, err := strconv.Atoi(sizeText)
	return size, err == nil && size >= 1 && size <= MaxRequestBytes
}

func writeControl(ctx context.Context, stream Stream, p []byte) error {
	deadline := time.Now().Add(writeTimeout)
	if overall, ok := ctx.Deadline(); ok && overall.Before(deadline) {
		deadline = overall
	}
	if ctx.Err() != nil || stream.SetWriteDeadline(deadline) != nil {
		return ErrProtocol
	}
	for len(p) != 0 {
		n, err := stream.Write(p)
		if n > 0 {
			p = p[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func refuse(ctx context.Context, stream Stream, cause error) error {
	if ctx.Err() == nil {
		_ = writeControl(ctx, stream, []byte("\x02upload refused\n"))
	}
	return cause
}
