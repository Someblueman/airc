package service

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"
)

func TestDTLSPeerReadDeadlineAndPinnedWrite(t *testing.T) {
	l, peer, client := newDTLSPeerForTest(t)
	defer closeDTLSPeerTest(l, peer, client)
	if _, _, err := peer.ReadFrom(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if err := peer.SetReadDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	if _, _, err := peer.ReadFrom(buf); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("ReadFrom deadline=%v", err)
	}
	if err := peer.SetWriteDeadline(time.Now().Add(40 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	l.writeSlot <- struct{}{}
	writeResult := make(chan error, 1)
	go func() {
		_, err := peer.WriteTo([]byte("x"), peer.RemoteAddr())
		writeResult <- err
	}()
	select {
	case err := <-writeResult:
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("WriteTo deadline=%v", err)
		}
	case <-time.After(time.Second):
		<-l.writeSlot
		t.Fatal("WriteTo did not honor its deadline")
	}
	<-l.writeSlot
	if _, err := peer.WriteTo([]byte("x"), &net.UDPAddr{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("foreign WriteTo=%v", err)
	}
}

func TestDTLSPacketListenerRoutesKnownApplicationRecordAndDropsNewestOnOverflow(t *testing.T) {
	l, peer, client := newDTLSPeerForTest(t)
	defer closeDTLSPeerTest(l, peer, client)
	if _, _, err := peer.ReadFrom(make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	for payload := byte(1); payload <= dtlsPeerQueueSize; payload++ {
		sendDTLSTestPacket(t, client, l.Addr(), testDTLSApplication(payload))
	}
	deadline := time.Now().Add(time.Second)
	for len(peer.packets) < dtlsPeerQueueSize && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := len(peer.packets); got != dtlsPeerQueueSize {
		t.Fatalf("queue fill=%d want=%d", got, dtlsPeerQueueSize)
	}
	sendDTLSTestPacket(t, client, l.Addr(), testDTLSApplication(0xff))
	time.Sleep(20 * time.Millisecond)
	if got := len(peer.packets); got != dtlsPeerQueueSize {
		t.Fatalf("overflow changed queue length=%d want=%d", got, dtlsPeerQueueSize)
	}
	for want := byte(1); want <= dtlsPeerQueueSize; want++ {
		buf := make([]byte, 64)
		n, _, err := peer.ReadFrom(buf)
		if err != nil {
			t.Fatal(err)
		}
		if n != 14 || buf[13] != want {
			t.Fatalf("queue order payload=%d want=%d", buf[13], want)
		}
	}
}
