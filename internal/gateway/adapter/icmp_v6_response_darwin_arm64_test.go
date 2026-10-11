//go:build darwin && arm64

package adapter

import (
	"bytes"
	"testing"
	"time"
)

func TestICMPv6CarriesMaximumResponseFragments(t *testing.T) {
	sender := icmpV6Fixture(t, 23957)
	receiver := icmpV6Fixture(t, 23958)
	response := bytes.Repeat([]byte{0x63}, MaxResponseFrameBytes)
	fragments, err := splitFragments(response)
	if err != nil {
		t.Fatal(err)
	}
	var reconstructed []byte
	for index, fragment := range fragments {
		// The socket transports both requests and responses. A response's
		// declared total can exceed the packet dispatcher's request limit.
		if err := sender.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if n, err := sender.WriteTo(fragment, receiver.LocalAddr()); err != nil || n != len(fragment) {
			t.Fatalf("write response fragment %d: n=%d err=%v", index, n, err)
		}
		if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, fragmentDatagramBytes)
		n, source, err := receiver.ReadFrom(got)
		if err != nil || n != len(fragment) || !bytes.Equal(got[:n], fragment) || source.String() != sender.LocalAddr().String() {
			t.Fatalf("read response fragment %d: n=%d source=%v err=%v", index, n, source, err)
		}
		reconstructed = append(reconstructed, got[fragmentHeaderBytes:n]...)
	}
	if !bytes.Equal(reconstructed, response) {
		t.Fatal("maximum response changed during native packet transport")
	}
}

func TestICMPv6QueuesCompleteRequestBurst(t *testing.T) {
	sender := icmpV6Fixture(t, 23967)
	receiver := icmpV6Fixture(t, 23968)
	fragments, err := splitFragments(bytes.Repeat([]byte{0x72}, MaxRequestFrameBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	// Deliberately queue the whole request before reading. The default 8 KiB
	// ICMP buffer loses fragments here even on an otherwise quiet loopback.
	for index, fragment := range fragments {
		if n, err := sender.WriteTo(fragment, receiver.LocalAddr()); err != nil || n != len(fragment) {
			t.Fatalf("write queued fragment %d: n=%d err=%v", index, n, err)
		}
	}
	if err := receiver.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool, len(fragments))
	for range fragments {
		body := make([]byte, fragmentDatagramBytes)
		n, source, err := receiver.ReadFrom(body)
		if err != nil {
			t.Fatalf("queued request lost a fragment after %d/%d: %v", len(seen), len(fragments), err)
		}
		if source.String() != sender.LocalAddr().String() || seen[string(body[:n])] {
			t.Fatal("queued request has an unexpected source or duplicate fragment")
		}
		seen[string(body[:n])] = true
	}
	for _, fragment := range fragments {
		if !seen[string(fragment)] {
			t.Fatal("queued request fragment changed")
		}
	}
}
