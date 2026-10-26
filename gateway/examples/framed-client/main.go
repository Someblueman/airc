// framed-client submits one plain contract request (JSON on stdin, naming its
// author) as a framed-stream frame and prints each response frame.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("address", "", "numeric gateway IP:port or absolute Unix socket path")
	network := flag.String("network", "tcp", "tcp or unix")
	ca := flag.String("ca", "", "trusted TLS CA PEM file (TCP only; omit for plaintext)")
	serverName := flag.String("server-name", "", "TLS certificate name (default: address IP)")
	correlation := flag.String("correlation", "request-1", "connection-local correlation ID")
	stream := flag.Bool("stream", false, "stream the watch request")
	flag.Parse()
	if flag.NArg() != 0 || *address == "" || (*network != "tcp" && *network != "unix") {
		return errors.New("address required; network is tcp or unix")
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, adapter.MaxRequestFrameBytes+1))
	if err != nil {
		return err
	}
	var contract map[string]json.RawMessage
	if json.Unmarshal(body, &contract) != nil {
		return errors.New("stdin must be one JSON contract request")
	}
	request, err := json.Marshal(struct {
		adapter.FrameRequest
		Request json.RawMessage `json:"request"`
	}{adapter.FrameRequest{Version: 1, CorrelationID: *correlation, Profile: "plain", Stream: *stream}, json.RawMessage(bytes.TrimSpace(body))})
	if err != nil || len(request) > adapter.MaxRequestFrameBytes {
		return errors.New("request does not fit a complete 16 KiB frame")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	var conn net.Conn
	if *network == "unix" {
		if !filepath.IsAbs(*address) || filepath.Clean(*address) != *address || *ca != "" || *serverName != "" {
			return errors.New("unix requires an absolute normalized path and no TLS options")
		}
		conn, err = dialer.Dial("unix", *address)
	} else {
		host, _, addressErr := net.SplitHostPort(*address)
		if addressErr != nil || net.ParseIP(host) == nil {
			return errors.New("explicit numeric IP:port required")
		}
		if *ca == "" {
			// Like IRC, plaintext is allowed; an account token in the
			// request is then visible on the network.
			conn, err = dialer.Dial("tcp", *address)
		} else {
			pem, readErr := os.ReadFile(*ca)
			if readErr != nil {
				return readErr
			}
			roots := x509.NewCertPool()
			if !roots.AppendCertsFromPEM(pem) {
				return errors.New("invalid CA file")
			}
			conn, err = tls.DialWithDialer(&dialer, "tcp", *address, &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: *serverName, NextProtos: []string{adapter.FramedALPN}})
		}
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	if secure, ok := conn.(*tls.Conn); ok && secure.ConnectionState().NegotiatedProtocol != adapter.FramedALPN {
		return errors.New("gateway ALPN not negotiated")
	}
	_ = conn.SetDeadline(time.Now().Add(25 * time.Second))
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(request)))
	wire := append(header[:], request...)
	for len(wire) > 0 {
		n, e := conn.Write(wire)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		wire = wire[n:]
	}
	total := 0
	for frames := 0; frames <= adapter.MaxStreamFrames; frames++ {
		if _, err = io.ReadFull(conn, header[:]); err != nil {
			return err
		}
		size := binary.BigEndian.Uint32(header[:])
		total += int(size) + 4
		if size == 0 || size > adapter.MaxResponseFrameBytes || total > adapter.MaxStreamBytes+adapter.MaxResponseFrameBytes {
			return errors.New("response exceeded frame/stream bounds")
		}
		data := make([]byte, size)
		if _, err = io.ReadFull(conn, data); err != nil {
			return err
		}
		var response adapter.FrameResponse
		if err = json.Unmarshal(data, &response); err != nil || response.Version != 1 || response.CorrelationID != *correlation {
			return errors.New("invalid or uncorrelated response")
		}
		if _, err = fmt.Fprintln(os.Stdout, string(data)); err != nil {
			return err
		}
		switch response.Event {
		case "error":
			return errors.New("gateway rejected request; typed failure is on stdout")
		case "result", "end":
			return nil
		case "page":
			if !*stream {
				return errors.New("unexpected stream page")
			}
		default:
			return errors.New("unknown response event")
		}
	}
	return errors.New("response exceeded frame count")
}
