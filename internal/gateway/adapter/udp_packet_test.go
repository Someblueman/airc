package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/testfs"
)

func TestPacketDestinationValidation(t *testing.T) {
	var nilUDP *net.UDPAddr
	var nilUnix *net.UnixAddr
	if packetAddrKey(nil) != "unnamed:" || packetAddrKey(nilUDP) != "unnamed:udp" || packetAddrKey(nilUnix) != "unnamed:unix" {
		t.Fatal("nil packet addresses were not assigned bounded unnamed keys")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.IPv4zero, Port: 9000}) {
		t.Fatal("accepted unspecified UDP destination")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.IPv4bcast, Port: 9000}) {
		t.Fatal("accepted broadcast UDP destination")
	}
	if validPacketDestination(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: -1}) || validPacketDestination(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 65536}) || validPacketDestination(&net.UDPAddr{IP: net.IP{1, 2, 3}, Port: 9000}) {
		t.Fatal("accepted invalid UDP destination bounds")
	}
	path := filepath.Join("/tmp", "airc-reply-test.sock")
	if !validPacketDestination(&net.UnixAddr{Name: path, Net: "unixgram"}) {
		t.Fatal("rejected absolute unixgram destination")
	}
	if validPacketDestination(&net.UnixAddr{Name: filepath.Join(".", path), Net: "unixgram"}) {
		t.Fatal("accepted noncanonical unixgram destination")
	}
	if validPacketDestination(&net.UnixAddr{Name: path, Net: "unix"}) {
		t.Fatal("accepted stream unix destination")
	}
}

func TestPacketDatagramsRejectsTruncatedPacketBeforeSourceLease(t *testing.T) {
	dir, err := testfs.MkdirTemp("agw-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server, err := net.ListenPacket("unixgram", filepath.Join(dir, "server.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	u := newUnixPacketAdapter(t, server.LocalAddr())
	client, err := net.ListenPacket("unixgram", filepath.Join(dir, "client.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- u.Serve(ctx, server) }()
	packet := make([]byte, agd1DatagramBytes+1)
	if _, err := client.WriteTo(packet, server.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	u.mu.Lock()
	gotSources, gotSlots := len(u.sources), len(u.slots)
	u.mu.Unlock()
	if gotSources != 0 || gotSlots != 0 {
		t.Fatalf("truncated packet retained source=%d slots=%d", gotSources, gotSlots)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("packet adapter did not join")
	}
}

func newUnixPacketAdapter(t *testing.T, destination net.Addr) *PacketDatagrams {
	t.Helper()
	_, signing, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := auth.NewResponseSealer("response", signing)
	if err != nil {
		t.Fatal(err)
	}
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	u, err := NewPacketDatagrams(&dispatch.Dispatcher{}, sealer, "realm", []PacketReturnRoute{{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "recipient", Destination: destination, Recipient: &recipient.PublicKey}}, make(chan struct{}, 2), make(chan struct{}, 2))
	if err != nil {
		t.Fatal(err)
	}
	return u
}
