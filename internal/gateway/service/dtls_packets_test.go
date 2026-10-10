package service

import (
	"encoding/binary"
	"net"
	"testing"
)

func testDTLSClientHello() []byte {
	packet := make([]byte, 13+12)
	packet[0], packet[1], packet[2] = 22, 0xfe, 0xfd
	binary.BigEndian.PutUint16(packet[3:], 0)
	binary.BigEndian.PutUint16(packet[11:], 12)
	packet[13] = 1
	return packet
}

func testDTLSApplication(payload byte) []byte {
	packet := make([]byte, 14)
	packet[0], packet[1], packet[2] = 23, 0xfe, 0xfd
	binary.BigEndian.PutUint16(packet[11:], 1)
	packet[13] = payload
	return packet
}

func sendDTLSTestPacket(t *testing.T, conn *net.UDPConn, address net.Addr, packet []byte) {
	t.Helper()
	if _, err := conn.WriteToUDP(packet, address.(*net.UDPAddr)); err != nil {
		t.Fatal(err)
	}
}

func acceptPeerAsync(l *dtlsPacketListener) <-chan *dtlsPeer {
	result := make(chan *dtlsPeer, 1)
	go func() {
		p, _, _ := l.Accept()
		if p == nil {
			result <- nil
			return
		}
		result <- p.(*dtlsPeer)
	}()
	return result
}

func acceptConnAsync(l *dtlsPacketListener) <-chan net.PacketConn {
	result := make(chan net.PacketConn, 1)
	go func() {
		p, _, _ := l.Accept()
		result <- p
	}()
	return result
}
