//go:build linux && arm64

package adapter

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSCTPOrderedTTLWriteAbandonsBoundedRecordAndRecovers(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrderedTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client := sctpTestClientWithTTL(t, listener.Addr(), 4096, 512)
	defer client.close()
	event := make([]byte, 8) // Linux struct sctp_event for partial-delivery abort.
	binary.LittleEndian.PutUint16(event[4:6], sctpPartialDeliveryEvent)
	event[6] = 1
	if err := unix.SetsockoptString(int(client.file.Fd()), unix.IPPROTO_SCTP, sctpOptionEvent, string(event)); err != nil {
		t.Fatal(err)
	}
	accepted := acceptSCTPTestConn(t, listener).(*sctpConn)
	defer accepted.Close()

	var optionErr error
	if err := accepted.raw.Control(func(fd uintptr) {
		optionErr = unix.SetsockoptInt(int(fd), unix.IPPROTO_SCTP, 13, 512)
	}); err != nil {
		t.Fatal(err)
	}
	if optionErr != nil {
		t.Fatal(optionErr)
	}
	_, sentBefore := sctpPRAbandonStats(t, accepted)
	payload := make([]byte, sctpRecordLimit)
	for i := range payload {
		payload[i] = byte('A' + i%26)
	}
	if n, err := accepted.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("production TTL Write = %d/%d, %v", n, len(payload), err)
	}
	waitSCTPAbandonedSent(t, accepted, sentBefore)

	if err := unix.SetsockoptInt(int(client.file.Fd()), unix.SOL_SOCKET, unix.SO_RCVBUF, 1<<20); err != nil {
		t.Fatal(err)
	}
	receiveSCTPPartialAbort(t, client)
	marker := []byte("whole-record-after-expiry")
	if n, err := accepted.Write(marker); err != nil || n != len(marker) {
		t.Fatalf("recovery TTL Write = %d/%d, %v", n, len(marker), err)
	}
	receiveSCTPWholeRecord(t, client, marker, 4*time.Second)
}

func TestSCTPOrderedTTLRejectsPRDisabledPeerAndContinuesAccept(t *testing.T) {
	listener, err := ListenSCTP("127.0.0.1:0", SCTPOrderedTTL)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	unsupported := sctpTestClientWithPRDisabled(t, listener.Addr())
	defer unsupported.close()
	accepted := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := listener.Accept()
		accepted <- struct {
			conn net.Conn
			err  error
		}{conn, err}
	}()
	valid := sctpTestClientWithTTL(t, listener.Addr(), 0, 512)
	defer valid.close()
	select {
	case result := <-accepted:
		if result.err != nil {
			t.Fatal(result.err)
		}
		defer result.conn.Close()
		clientLocal, err := unix.Getsockname(int(valid.file.Fd()))
		if err != nil {
			t.Fatal(err)
		}
		acceptedPeer, ok := result.conn.RemoteAddr().(sctpIPAddr)
		if !ok || acceptedPeer.port != clientLocal.(*unix.SockaddrInet4).Port {
			t.Fatalf("listener returned peer %v, want PR-enabled client source %v", result.conn.RemoteAddr(), clientLocal)
		}
		if err := result.conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		valid.sendInfo(t, []byte("accepted-after-disabled-peer"), 0, false)
		buffer := make([]byte, 128)
		n, err := result.conn.Read(buffer)
		if err != nil || string(buffer[:n]) != "accepted-after-disabled-peer" {
			t.Fatalf("valid peer record = %q, %v", buffer[:n], err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("listener did not continue after PR-disabled peer")
	}
}
