// udp-anonymous-client sends a plaintext JSON request to an explicitly anonymous
// UDP gateway. It needs no credential files, fixed local port, or enrollment.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("address", "", "numeric anonymous gateway UDP IP:port")
	budget := flag.Int("max-response-bytes", 4096, "request/response datagram budget (512-16384 bytes)")
	flag.Parse()
	endpoint, err := netip.ParseAddrPort(*address)
	if err != nil || endpoint.Port() == 0 || endpoint.Addr().IsUnspecified() || endpoint.Addr().IsMulticast() || endpoint.Addr() == netip.AddrFrom4([4]byte{255, 255, 255, 255}) || flag.NArg() != 0 {
		return errors.New("a numeric unicast --address with nonzero port is required")
	}
	if *budget < 512 || *budget > core.MaxEnvelopeBytes {
		return errors.New("max-response-bytes must be between 512 and 16384")
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, core.MaxEnvelopeBytes+1))
	if err != nil {
		return err
	}
	request, err := core.Decode(data)
	if err != nil {
		return err
	}
	if len(data) > *budget {
		return errors.New("request exceeds datagram budget; increase --max-response-bytes")
	}
	// Whitespace lets the server return a useful page without amplifying UDP
	// traffic. It changes neither the request intent nor its durable identity.
	data = append(data, bytes.Repeat([]byte(" "), *budget-len(data))...)
	conn, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(endpoint))
	if err != nil {
		return err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	buf := make([]byte, *budget+1)
	n, err := conn.Read(buf)
	if err != nil {
		return err
	}
	var response adapter.AnonymousUDPResponse
	// Receipts deliberately contain nullable acceptance fields until the native
	// server has answered. Validate syntax without applying request null rules.
	if n > *budget || jsoninput.Validate(buf[:n], *budget) != nil || json.Unmarshal(buf[:n], &response) != nil || response.Version != 1 || response.RequestID != request.RequestID || (response.Result == nil) == (response.Error == nil) {
		return errors.New("invalid anonymous UDP response")
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		return err
	}
	if response.Error != nil {
		return fmt.Errorf("gateway: %s", response.Error.Code)
	}
	return nil
}
