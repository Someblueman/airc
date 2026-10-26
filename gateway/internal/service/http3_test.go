//go:build darwin || linux

package service

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Someblueman/airc/gateway/internal/adapter"
	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/internal/testtls"
	"github.com/quic-go/quic-go"
)

func TestHTTP3HandshakeAdmissionSharesAndReleasesServiceSlots(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	slots := make(chan struct{}, 1)
	owner, err := newHTTP3Owner(context.Background(), testDispatcher(t), slots, cap(slots), nil, pair, "127.0.0.1:0", adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := owner.close(ctx); err != nil {
			t.Fatal(err)
		}
	}()

	// The shared slot is the same admission boundary used by TCP listeners.
	slots <- struct{}{}
	if _, err := owner.transport.ConnContext(context.Background(), &quic.ClientInfo{}); err == nil {
		t.Fatal("HTTP/3 handshake bypassed the shared connection slot")
	}
	<-slots

	base, cancelBase := context.WithCancel(context.Background())
	accepted, err := owner.transport.ConnContext(base, &quic.ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	cancelBase()
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("handshake slot was not released when base context closed")
		default:
			select {
			case slots <- struct{}{}:
				<-slots
				_ = accepted
				return
			default:
				time.Sleep(time.Millisecond)
			}
		}
	}
}

func TestHTTP3OwnerCloseReleasesTransportWithoutServing(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newHTTP3Owner(context.Background(), testDispatcher(t), make(chan struct{}, 2), 2, nil, pair, "127.0.0.1:0", adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	address := owner.addr().String()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := owner.close(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal("repeated owner close changed result", err)
	}
	packet, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("closed owner retained UDP socket: %v", err)
	}
	_ = packet.Close()
}

func TestHTTP3OwnerServeJoinsOnShutdown(t *testing.T) {
	cert := testtls.New(t)
	pair, err := auth.ServerCertificate(cert.CertPath, cert.KeyPath)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := newHTTP3Owner(context.Background(), testDispatcher(t), make(chan struct{}, 2), 2, nil, pair, "127.0.0.1:0", adapter.Keepalive{})
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- owner.serve(context.Background()) }()
	deadline := time.After(time.Second)
	for {
		owner.mu.Lock()
		started := owner.serveStarted
		owner.mu.Unlock()
		if started {
			break
		}
		select {
		case <-deadline:
			t.Fatal("HTTP/3 accept loop did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := owner.close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP/3 accept loop did not join")
	}
}
