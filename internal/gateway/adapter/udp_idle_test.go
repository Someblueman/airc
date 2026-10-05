package adapter

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestUDPInvalidTrafficCannotRetainAdmissionLease(t *testing.T) {
	u := newUDPTestAdapter(t)
	server, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- u.Serve(ctx, server) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Error("UDP owner did not join")
		}
	}()
	address := client.LocalAddr().(*net.UDPAddr).AddrPort()
	var original *udpSource
	packets := 0
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		// A new transport ID avoids completion-cache suppression. The valid
		// AGD1 record carries invalid application data, so it cannot renew.
		fragments, err := fragmentAGD1([]byte("invalid-application"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.WriteToUDP(fragments[0], server.LocalAddr().(*net.UDPAddr)); err != nil {
			t.Fatal(err)
		}
		packets++
		time.Sleep(50 * time.Millisecond)
		u.mu.Lock()
		current := u.sources[address]
		if original == nil {
			original = current
		}
		replaced := current != nil && original != nil && current != original
		u.mu.Unlock()
		if replaced {
			if packets < 20 {
				t.Fatalf("lease replaced before sustained input: packets=%d", packets)
			}
			return
		}
	}
	t.Fatal("continuous invalid application traffic retained its admission lease")
}
