//go:build windows

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/winpipe"
)

func runChild(ctx context.Context, command string, o options) error {
	if command == "request" {
		return request(ctx, o)
	}
	return serve(ctx, o)
}

func request(ctx context.Context, o options) (result error) {
	data, err := requestBytes(o)
	if err != nil {
		return err
	}
	pipe, err := winpipe.Dial(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	if err := pipe.WriteFrame(ctx, data, maxRequest); err != nil {
		return err
	}
	response, err := pipe.ReadFrame(ctx, maxResponse)
	if err != nil {
		return err
	}
	if err := validateResponse(response, o.correlation); err != nil {
		return err
	}
	// The server waits for consumption before closing its pipe instance.
	if err := pipe.WriteFrame(ctx, []byte{1}, 1); err != nil {
		return err
	}
	_, err = os.Stdout.Write(response)
	return err
}

func serve(ctx context.Context, o options) (result error) {
	pipe, err := winpipe.Accept(ctx, o.pipe, o.serverSID, o.clientSID)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, pipe.Close()) }()
	readCtx, cancelRead := context.WithTimeout(ctx, 5*time.Second)
	data, err := pipe.ReadFrame(readCtx, maxRequest)
	cancelRead()
	if err != nil {
		return err
	}
	if err := pipe.VerifyClient(); err != nil {
		return err
	}
	var frame frameRequest
	if json.Unmarshal(data, &frame) != nil || frame.Version != 1 || frame.Stream {
		return errors.New("pipe bridge requires one nonstreaming framed request")
	}
	response, err := forwardTLS(ctx, o, data)
	if err != nil {
		return err
	}
	if err := validateResponse(response, frame.CorrelationID); err != nil {
		return err
	}
	if err := pipe.WriteFrame(ctx, response, maxResponse); err != nil {
		return err
	}
	ack, err := pipe.ReadFrame(ctx, 1)
	if err != nil || len(ack) != 1 || ack[0] != 1 {
		return errors.New("client did not confirm consumption of the pipe response")
	}
	return nil
}

func forwardTLS(ctx context.Context, o options, request []byte) ([]byte, error) {
	ca, err := os.Open(o.ca)
	if err != nil {
		return nil, err
	}
	info, statErr := ca.Stat()
	if statErr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		ca.Close()
		return nil, errors.New("CA file must be bounded and regular")
	}
	pem, readErr := io.ReadAll(io.LimitReader(ca, (1<<20)+1))
	err = errors.Join(readErr, ca.Close())
	if err != nil || len(pem) > 1<<20 {
		return nil, errors.New("CA file read failed or exceeds bound")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("invalid gateway CA")
	}
	dialer := tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: o.serverName, NextProtos: []string{"airc-gateway/1"},
	}}
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	conn, err := dialer.DialContext(dialCtx, "tcp", o.address)
	cancel()
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if conn.(*tls.Conn).ConnectionState().NegotiatedProtocol != "airc-gateway/1" {
		return nil, errors.New("gateway TLS ALPN mismatch")
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return nil, errors.New("gateway operation requires deadline")
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(request)))
	wire := append(header[:], request...)
	for len(wire) > 0 {
		n, err := conn.Write(wire)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, io.ErrShortWrite
		}
		wire = wire[n:]
	}
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(header[:])
	if size < 1 || size > maxResponse {
		return nil, errors.New("gateway response exceeds frame bound")
	}
	response := make([]byte, size)
	_, err = io.ReadFull(conn, response)
	return response, err
}
