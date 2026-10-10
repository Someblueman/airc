// udp-client submits an already encrypted compact envelope on stdin and prints
// one authenticated gateway response as JSON. It never creates identities or
// chooses an upstream target.
package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/auth"
)

const (
	agdHeader = 28
	agdChunk  = 972
	agdMagic  = "AGD1"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	address := flag.String("address", "", "numeric gateway UDP IP:port")
	bind := flag.String("bind", "", "numeric fixed local mailbox IP:port")
	publicPath := flag.String("signing-public", "", "Ed25519 response signing public key PEM")
	recipientPath := flag.String("recipient-private", "", "P-256 response recipient private key PEM")
	realm := flag.String("realm", "", "expected response realm")
	principal := flag.String("principal", "", "expected response principal")
	binding := flag.String("binding", "", "expected response binding")
	route := flag.String("route", "", "expected response route")
	signingID := flag.String("signing-id", "", "expected response signing key ID")
	recipientID := flag.String("recipient-id", "", "expected response recipient key ID")
	correlation := flag.String("correlation", "", "fresh correlation ID (default: random per invocation)")
	flag.Parse()
	if flag.NArg() != 0 || *address == "" || *bind == "" || *publicPath == "" || *recipientPath == "" || *realm == "" || *principal == "" || *binding == "" || *route == "" || *signingID == "" || *recipientID == "" {
		return errors.New("address, fixed mailbox, response scope, and strict response key files are required")
	}
	if *correlation == "" {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return err
		}
		*correlation = "udp-" + hex.EncodeToString(nonce[:])
	}
	destination, err := numericUDPAddr(*address)
	if err != nil {
		return err
	}
	if destination.Port == 0 || destination.IP.IsUnspecified() || destination.IP.IsMulticast() || destination.IP.Equal(net.IPv4bcast) || destination.IP.Equal(net.IPv4(255, 255, 255, 255)) {
		return errors.New("gateway address must be a nonzero numeric unicast IP:port")
	}
	local, err := numericUDPAddr(*bind)
	if err != nil {
		return err
	}
	if local.Port == 0 || local.IP.IsMulticast() || local.IP.Equal(net.IPv4bcast) {
		return errors.New("fixed mailbox requires a nonzero unicast or wildcard bind port")
	}
	signingKey, err := readEd25519Public(*publicPath)
	if err != nil {
		return err
	}
	recipient, err := readP256Private(*recipientPath)
	if err != nil {
		return err
	}
	envelope, err := io.ReadAll(io.LimitReader(os.Stdin, adapter.MaxRequestFrameBytes+1))
	if err != nil {
		return err
	}
	envelope = bytes.TrimSuffix(envelope, []byte("\n"))
	if len(envelope) == 0 {
		return errors.New("encrypted envelope is empty")
	}
	frame, err := json.Marshal(adapter.FrameRequest{Version: 1, CorrelationID: *correlation, Profile: "encrypted", Envelope: string(envelope)})
	if err != nil || len(frame) > adapter.MaxRequestFrameBytes {
		return errors.New("encrypted request does not fit the 16 KiB frame bound")
	}
	conn, err := net.ListenUDP("udp", local)
	if err != nil {
		return err
	}
	defer conn.Close()
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	fragments := fragment(frame, id)
	deadline := time.Now().Add(3 * time.Second)
	for _, part := range fragments {
		if err := conn.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if _, err := conn.WriteToUDP(part, destination); err != nil {
			return err
		}
	}
	token, err := readResponse(conn, time.Now().Add(5*time.Second))
	if err != nil {
		return err
	}
	plain, err := auth.OpenResponse(token, *signingID, signingKey, *recipientID, recipient, auth.ResponseScope{Realm: *realm, Principal: *principal, Binding: *binding, Route: *route}, time.Now())
	if err != nil {
		return err
	}
	if len(plain) > auth.MaxResponsePayloadBytes {
		return errors.New("authenticated response exceeded payload bound")
	}
	var response adapter.FrameResponse
	if err := json.Unmarshal(plain, &response); err != nil || response.Version != 1 || response.CorrelationID != *correlation || (response.Event != "result" && response.Event != "error") || response.Sequence != 1 {
		return errors.New("invalid authenticated gateway response")
	}
	if (response.Event == "result" && (response.Result == nil || response.Error != nil)) || (response.Event == "error" && (response.Error == nil || response.Result != nil)) {
		return errors.New("authenticated gateway response is missing its result or failure")
	}
	data, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(os.Stdout, string(data))
	return err
}

func numericUDPAddr(value string) (*net.UDPAddr, error) {
	parsed, err := netip.ParseAddrPort(value)
	if err != nil || !parsed.Addr().IsValid() {
		return nil, errors.New("numeric UDP IP:port required")
	}
	return net.ResolveUDPAddr("udp", value)
}

func readEd25519Public(path string) (ed25519.PublicKey, error) {
	data, err := strictKeyFile(path, false)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if !bytes.HasPrefix(data, []byte("-----BEGIN PUBLIC KEY-----")) {
		return nil, errors.New("public key has leading data")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid Ed25519 public key PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	defer clear(block.Bytes)
	public, ok := key.(ed25519.PublicKey)
	if err != nil || !ok || len(public) != ed25519.PublicKeySize {
		return nil, errors.New("response signing key is not Ed25519")
	}
	return append(ed25519.PublicKey(nil), public...), nil
}

func readP256Private(path string) (*ecdsa.PrivateKey, error) {
	data, err := strictKeyFile(path, true)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if !bytes.HasPrefix(data, []byte("-----BEGIN PRIVATE KEY-----")) {
		return nil, errors.New("private key has leading data")
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
		return nil, errors.New("invalid P-256 private key PEM")
	}
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	defer clear(block.Bytes)
	private, ok := key.(*ecdsa.PrivateKey)
	if err != nil || !ok || private.Curve != elliptic.P256() {
		return nil, errors.New("response recipient key is not P-256")
	}
	return private, nil
}

func fragment(body []byte, id [16]byte) [][]byte {
	count := (len(body) + agdChunk - 1) / agdChunk
	parts := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		start := index * agdChunk
		end := start + agdChunk
		if end > len(body) {
			end = len(body)
		}
		part := make([]byte, agdHeader+end-start)
		copy(part[:4], agdMagic)
		copy(part[4:20], id[:])
		binary.BigEndian.PutUint32(part[20:24], uint32(len(body)))
		binary.BigEndian.PutUint16(part[24:26], uint16(index))
		binary.BigEndian.PutUint16(part[26:28], uint16(count))
		copy(part[agdHeader:], body[start:end])
		parts = append(parts, part)
	}
	return parts
}

func readResponse(conn *net.UDPConn, deadline time.Time) (string, error) {
	var id [16]byte
	var total, count int
	parts := make(map[int][]byte)
	for count == 0 || len(parts) < count {
		if err := conn.SetReadDeadline(deadline); err != nil {
			return "", err
		}
		buf := make([]byte, 1001)
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return "", err
		}
		if n > 1000 {
			return "", errors.New("oversized AGD1 response datagram")
		}
		buf = buf[:n]
		if len(buf) < agdHeader || string(buf[:4]) != agdMagic {
			return "", errors.New("invalid AGD1 response fragment")
		}
		if count == 0 {
			copy(id[:], buf[4:20])
			total, count = int(binary.BigEndian.Uint32(buf[20:24])), int(binary.BigEndian.Uint16(buf[26:28]))
			if total < 1 || total > 64<<10 || count < 1 || count != (total+agdChunk-1)/agdChunk {
				return "", errors.New("invalid AGD1 response bounds")
			}
		}
		index := int(binary.BigEndian.Uint16(buf[24:26]))
		if int(binary.BigEndian.Uint32(buf[20:24])) != total || int(binary.BigEndian.Uint16(buf[26:28])) != count || !bytes.Equal(buf[4:20], id[:]) || index >= count {
			return "", errors.New("inconsistent AGD1 response fragment")
		}
		payload := buf[agdHeader:]
		want := agdChunk
		if index == count-1 {
			want = total - agdChunk*(count-1)
		}
		if len(payload) != want {
			return "", errors.New("invalid AGD1 response fragment size")
		}
		if prior, exists := parts[index]; exists {
			if !bytes.Equal(prior, payload) {
				return "", errors.New("conflicting AGD1 response duplicate")
			}
			continue
		}
		parts[index] = append([]byte(nil), payload...)
	}
	result := make([]byte, 0, total)
	for index := 0; index < count; index++ {
		part, ok := parts[index]
		if !ok {
			return "", errors.New("missing AGD1 response fragment")
		}
		result = append(result, part...)
	}
	return string(result), nil
}
