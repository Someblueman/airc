// framed-client submits an already signed/encrypted compact envelope on stdin.
// It does not create identities or choose upstream accounts/targets.
package main

import (
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

	"github.com/Someblueman/airc/internal/gateway/adapter"
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
	ca := flag.String("ca", "", "trusted TLS CA PEM file")
	serverName := flag.String("server-name", "", "TLS certificate name (default: address IP)")
	local := flag.Bool("local-plaintext", false, "loopback TCP only; responses are unencrypted")
	profile := flag.String("profile", "signed", "signed or encrypted")
	correlation := flag.String("correlation", "request-1", "connection-local correlation ID")
	stream := flag.Bool("stream", false, "stream the authenticated watch request")
	flag.Parse()
	if flag.NArg() != 0 || *address == "" || (*network != "tcp" && *network != "unix") || (*profile != "signed" && *profile != "encrypted") {
		return errors.New("address and fixed signed/encrypted profile required")
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, adapter.MaxRequestFrameBytes+1))
	if err != nil {
		return err
	}
	// Compact JOSE has no whitespace; a terminal newline from a private file is OK.
	if len(body) > 0 && body[len(body)-1] == '\n' {
		body = body[:len(body)-1]
	}
	request, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: *correlation, Profile: *profile, Envelope: string(body), Stream: *stream})
	if err != nil || len(body) == 0 || len(request) > adapter.MaxRequestFrameBytes {
		return errors.New("envelope does not fit complete 16 KiB request frame")
	}
	dialer := net.Dialer{Timeout: 3 * time.Second}
	var conn net.Conn
	if *network == "unix" {
		if !filepath.IsAbs(*address) || filepath.Clean(*address) != *address || *profile != "encrypted" || *ca != "" || *serverName != "" || *local {
			return errors.New("unix requires an absolute normalized path, encrypted envelope, and no TCP/TLS options")
		}
		conn, err = dialer.Dial("unix", *address)
	} else {
		host, _, addressErr := net.SplitHostPort(*address)
		if addressErr != nil || net.ParseIP(host) == nil {
			return errors.New("explicit numeric IP:port required")
		}
		if *local {
			if !net.ParseIP(host).IsLoopback() || *profile != "encrypted" || *ca != "" || *serverName != "" {
				return errors.New("plaintext requires loopback, encrypted envelope, and no TLS options")
			}
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
