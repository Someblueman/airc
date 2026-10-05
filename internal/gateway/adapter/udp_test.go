package adapter

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
)

func newUDPTestAdapter(t *testing.T) *UDPDatagrams {
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
	u, err := NewUDPDatagrams(&dispatch.Dispatcher{}, sealer, "realm", []UDPReturnRoute{{ID: "route", Principal: "principal", Binding: "binding", RecipientID: "recipient", Destination: netip.MustParseAddrPort("127.0.0.1:9"), Recipient: &recipient.PublicKey}}, make(chan struct{}, 2), make(chan struct{}, 2))
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestUDPDatagramsRejectsInvalidConstruction(t *testing.T) {
	u := newUDPTestAdapter(t)
	if _, err := NewUDPDatagrams(nil, u.sealer, "realm", nil, make(chan struct{}, 1), make(chan struct{}, 1)); err != store.ErrInvalid {
		t.Fatalf("nil dispatcher error=%v", err)
	}
	duplicate := []UDPReturnRoute{
		{ID: "one", Principal: "principal", Binding: "binding", RecipientID: "recipient", Destination: netip.MustParseAddrPort("127.0.0.1:9"), Recipient: u.routes["principal\x00binding"].Recipient},
		{ID: "two", Principal: "principal", Binding: "other", RecipientID: "recipient", Destination: netip.MustParseAddrPort("127.0.0.1:10"), Recipient: u.routes["principal\x00binding"].Recipient},
	}
	if _, err := NewUDPDatagrams(&dispatch.Dispatcher{}, u.sealer, "realm", duplicate, make(chan struct{}, 1), make(chan struct{}, 1)); err != store.ErrInvalid {
		t.Fatalf("duplicate principal error=%v", err)
	}
}

func TestUDPDatagramsBoundsUnauthenticatedSourcesAndJoinsShutdown(t *testing.T) {
	u := newUDPTestAdapter(t)
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- u.Serve(ctx, server) }()
	clients := make([]*net.UDPConn, 0, 24)
	for i := 0; i < 24; i++ {
		client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		clients = append(clients, client)
		fragments, err := fragmentAGD1([]byte("partial-source"))
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(fragments[0], server.LocalAddr().(*net.UDPAddr)); err != nil {
			cancel()
			t.Fatal(err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	u.mu.Lock()
	if got, want := len(u.sources), cap(u.slots); got != want || len(u.slots) != want {
		u.mu.Unlock()
		cancel()
		t.Fatalf("unauthenticated source map=%d want=%d", got, want)
	}
	u.mu.Unlock()
	for _, client := range clients {
		client.Close()
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve shutdown=%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not join after context cancellation")
	}
	if got := len(u.slots); got != 0 {
		t.Fatalf("shutdown retained UDP source slots=%d", got)
	}
}
