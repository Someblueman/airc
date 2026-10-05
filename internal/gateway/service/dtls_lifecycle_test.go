package service

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

func newDTLSPeerForTest(t *testing.T) (*dtlsPacketListener, *dtlsPeer, *net.UDPConn) {
	t.Helper()
	l, err := newDTLSPacketListener("127.0.0.1:0", make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	client, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		l.Close()
		t.Fatal(err)
	}
	sendDTLSTestPacket(t, client, l.Addr(), testDTLSClientHello())
	conn, _, err := l.Accept()
	if err != nil {
		client.Close()
		l.Close()
		t.Fatal(err)
	}
	return l, conn.(*dtlsPeer), client
}

func closeDTLSPeerTest(l *dtlsPacketListener, p *dtlsPeer, client *net.UDPConn) {
	_ = p.Close()
	p.release()
	_ = client.Close()
	_ = l.Close()
}

func TestDTLSRawPeerPacketsDoNotRefreshAuthenticatedIdle(t *testing.T) {
	l, peer, client := newDTLSPeerForTest(t)
	defer closeDTLSPeerTest(l, peer, client)
	old := time.Now().Add(-dtlsPeerIdle + time.Second)
	l.mu.Lock()
	peer.lastActivity = old
	l.mu.Unlock()
	application := make([]byte, 14)
	application[0], application[1], application[2] = 23, 0xfe, 0xfd
	binary.BigEndian.PutUint16(application[11:], 1)
	application[13] = 7
	for i := 0; i < 3; i++ {
		sendDTLSTestPacket(t, client, l.Addr(), application)
		time.Sleep(10 * time.Millisecond)
	}
	l.mu.Lock()
	last := peer.lastActivity
	l.mu.Unlock()
	if !last.Equal(old) {
		t.Fatalf("raw peer packet refreshed idle timestamp: got=%s want=%s", last, old)
	}
}

func TestDTLSStaleReaperCutoffDoesNotCloseNewlyActivePeer(t *testing.T) {
	l, peer, client := newDTLSPeerForTest(t)
	defer closeDTLSPeerTest(l, peer, client)
	old := time.Now().Add(-dtlsPeerIdle - time.Second)
	l.mu.Lock()
	peer.lastActivity = old
	l.mu.Unlock()
	peer.activity()
	l.closePeer(peer, old)
	select {
	case <-peer.closed:
		t.Fatal("stale cutoff closed a peer active after snapshot")
	default:
	}
}

func TestDTLSClosedPeerDeadlineCannotReviveWrites(t *testing.T) {
	l, peer, client := newDTLSPeerForTest(t)
	peer.Close()
	defer func() { peer.release(); _ = client.Close(); _ = l.Close() }()
	if err := peer.SetWriteDeadline(time.Now().Add(time.Second)); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("SetWriteDeadline after close=%v", err)
	}
	if _, err := peer.WriteTo([]byte("x"), peer.RemoteAddr()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("WriteTo after close=%v", err)
	}
}
