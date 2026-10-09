//go:build linux && arm64

package adapter

import (
	"encoding/binary"
	"net"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

func acceptSCTPTestConn(t *testing.T, listener net.Listener) net.Conn {
	t.Helper()
	done := make(chan struct {
		conn net.Conn
		err  error
	}, 1)
	go func() {
		conn, err := listener.Accept()
		done <- struct {
			conn net.Conn
			err  error
		}{conn, err}
	}()
	returned := <-done
	if returned.err != nil {
		t.Fatal(returned.err)
	}
	return returned.conn
}

type sctpTestPeer struct {
	file *os.File
}

func sctpTestClient(t *testing.T, address net.Addr) *sctpTestPeer {
	return sctpTestClientWithBuffers(t, address, 0, 0)
}

func sctpTestClientWithBuffers(t *testing.T, address net.Addr, sendBuffer, receiveBuffer int) *sctpTestPeer {
	t.Helper()
	tcpAddr, ok := address.(sctpIPAddr)
	if !ok {
		t.Fatalf("listener address type %T", address)
	}
	var sockaddr unix.Sockaddr
	if ip := tcpAddr.ip.To4(); ip != nil {
		var ip4 [4]byte
		copy(ip4[:], ip)
		sockaddr = &unix.SockaddrInet4{Addr: ip4, Port: tcpAddr.port}
	} else {
		var ip6 [16]byte
		copy(ip6[:], tcpAddr.ip.To16())
		sockaddr = &unix.SockaddrInet6{Addr: ip6, Port: tcpAddr.port}
	}
	family := unix.AF_INET
	if _, ok := sockaddr.(*unix.SockaddrInet6); ok {
		family = unix.AF_INET6
	}
	fd, err := unix.Socket(family, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, unix.IPPROTO_SCTP)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.SetsockoptInt(fd, unix.IPPROTO_SCTP, sctpOptionReceiveInfo, 1); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	if sendBuffer > 0 {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_SNDBUF, sendBuffer); err != nil {
			_ = unix.Close(fd)
			t.Fatal(err)
		}
	}
	if receiveBuffer > 0 {
		if err := unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, receiveBuffer); err != nil {
			_ = unix.Close(fd)
			t.Fatal(err)
		}
	}
	if err := unix.Connect(fd, sockaddr); err != nil {
		_ = unix.Close(fd)
		t.Fatal(err)
	}
	return &sctpTestPeer{file: os.NewFile(uintptr(fd), "sctp-test-client")}
}

func (p *sctpTestPeer) send(t *testing.T, data []byte) {
	t.Helper()
	n, err := unix.SendmsgN(int(p.file.Fd()), data, nil, nil, unix.MSG_NOSIGNAL)
	if err != nil || n != len(data) {
		t.Fatalf("client send = %d, %v", n, err)
	}
}

func (p *sctpTestPeer) sendMode(t *testing.T, data []byte, mode SCTPMode) {
	t.Helper()
	if mode == SCTPUnordered {
		p.sendInfo(t, data, sctpMessageUnordered, false)
		return
	}
	p.send(t, data)
}

func (p *sctpTestPeer) sendInfo(t *testing.T, data []byte, flags uint16, nonzeroPPID bool) {
	t.Helper()
	info := make([]byte, 16)
	binary.LittleEndian.PutUint16(info[2:4], flags)
	if nonzeroPPID {
		binary.BigEndian.PutUint32(info[4:8], 1)
	}
	oob := make([]byte, unix.CmsgSpace(len(info)))
	binary.LittleEndian.PutUint64(oob[:8], uint64(unix.CmsgLen(len(info))))
	binary.LittleEndian.PutUint32(oob[8:12], uint32(unix.IPPROTO_SCTP))
	binary.LittleEndian.PutUint32(oob[12:16], uint32(sctpSendInfoCmsg))
	copy(oob[unix.CmsgLen(0):], info)
	n, err := unix.SendmsgN(int(p.file.Fd()), data, oob, nil, unix.MSG_NOSIGNAL)
	if err != nil || n != len(data) {
		t.Fatalf("client sendmsg with SCTP_SNDINFO = %d, %v", n, err)
	}
}

func (p *sctpTestPeer) recv(t *testing.T, size int, mode SCTPMode) []byte {
	t.Helper()
	data := make([]byte, size)
	oob := make([]byte, unix.CmsgSpace(sctpReceiveInfoSize))
	ready := []unix.PollFd{{Fd: int32(p.file.Fd()), Events: unix.POLLIN}}
	if _, err := unix.Poll(ready, 3000); err != nil {
		t.Fatal(err)
	}
	if ready[0].Revents == 0 {
		t.Fatal("timeout waiting for client record")
	}
	n, oobn, flags, _, err := unix.Recvmsg(int(p.file.Fd()), data, oob, 0)
	if err != nil {
		t.Fatal(err)
	}
	if flags&unix.MSG_EOR == 0 {
		t.Fatalf("client recvmsg flags = %d, no EOR", flags)
	}
	if _, err := parseSCTPReceiveInfo(oob[:oobn], mode); err != nil {
		t.Fatalf("client receive metadata: %v", err)
	}
	return data[:n]
}

func (p *sctpTestPeer) close() { _ = p.file.Close() }
