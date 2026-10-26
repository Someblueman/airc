package service

import (
	"errors"
	"net"
	"testing"
	"time"
)

func TestDTLSPacketListenerFiltersMalformedAndOversize(t *testing.T) {
	l, err := newDTLSPacketListener("127.0.0.1:0", make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	defer client.Close()
	accepted := acceptPeerAsync(l)
	sendDTLSTestPacket(t, client, l.Addr(), []byte{1, 2, 3})
	sendDTLSTestPacket(t, client, l.Addr(), make([]byte, dtlsPacketMax+1))
	select {
	case p := <-accepted:
		_ = l.Close()
		if p != nil {
			_ = p.Close()
			p.release()
		}
		t.Fatal("malformed packet was admitted")
	case <-time.After(150 * time.Millisecond):
		_ = l.Close()
		if p := <-accepted; p != nil {
			_ = p.Close()
			p.release()
		}
	}
}

func TestDTLSPacketListenerAdmissionAndExplicitRelease(t *testing.T) {
	slots := make(chan struct{}, 1)
	l, err := newDTLSPacketListener("127.0.0.1:0", slots)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	c2, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		c1.Close()
		l.Close()
		t.Fatal(err)
	}
	defer c1.Close()
	defer c2.Close()
	sendDTLSTestPacket(t, c1, l.Addr(), testDTLSClientHello())
	conn, _, err := l.Accept()
	if err != nil {
		t.Fatal(err)
	}
	peer := conn.(*dtlsPeer)
	defer func() { _ = peer.Close(); peer.release(); _ = l.Close() }()
	if len(slots) != 1 {
		t.Fatalf("admission slots=%d", len(slots))
	}
	pending := acceptConnAsync(l)
	pendingConsumed := false
	defer func() {
		_ = l.Close()
		if !pendingConsumed {
			<-pending
		}
	}()
	sendDTLSTestPacket(t, c2, l.Addr(), testDTLSClientHello())
	select {
	case p := <-pending:
		pendingConsumed = true
		if p != nil {
			_ = p.Close()
		}
		t.Fatal("second peer bypassed the admission slot")
	case <-time.After(100 * time.Millisecond):
	}
	_ = peer.Close()
	if len(slots) != 1 {
		t.Fatal("closing an accepted peer released its slot early")
	}
	peer.release()
	sendDTLSTestPacket(t, c2, l.Addr(), testDTLSClientHello())
	select {
	case p := <-pending:
		pendingConsumed = true
		if p == nil {
			t.Fatal("accepted second peer is nil")
		}
		second := p.(*dtlsPeer)
		_ = second.Close()
		second.release()
	case <-time.After(time.Second):
		t.Fatal("released admission slot did not accept the second peer")
	}
}

func TestDTLSPacketListenerCloseJoinsAndRebinds(t *testing.T) {
	l, err := newDTLSPacketListener("127.0.0.1:0", make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after close=%v", err)
	}
	rebound, err := newDTLSPacketListener(address, make(chan struct{}, 1))
	if err != nil {
		t.Fatalf("rebind after joined close: %v", err)
	}
	_ = rebound.Close()
}
