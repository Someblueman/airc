//go:build darwin || linux

package gateway_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"syscall"
	"testing"
	"time"
)

// This client serializes packets independently of the production MQTT codec.
func mqttString(body []byte, field string) []byte {
	return append(binary.BigEndian.AppendUint16(body, uint16(len(field))), field...)
}
func mqttPacket(header byte, body []byte) []byte {
	packet := []byte{header}
	for size := len(body); ; size /= 128 {
		b := byte(size % 128)
		if size >= 128 {
			b |= 128
		}
		packet = append(packet, b)
		if size < 128 {
			break
		}
	}
	return append(packet, body...)
}
func mqttPublication(id uint16, topic, payload string, duplicate bool) []byte {
	body := binary.BigEndian.AppendUint16(mqttString(nil, topic), id)
	header := byte(0x32)
	if duplicate {
		header |= 8
	}
	return mqttPacket(header, append(body, payload...))
}
func mqttWrite(t *testing.T, conn net.Conn, packet []byte) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(conn, bytes.NewReader(packet)); err != nil {
		t.Fatal(err)
	}
}
func mqttReply(t *testing.T, conn net.Conn, want []byte) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, want) {
		t.Fatal("MQTT reply", got, err)
	}
}
func mqttPeerClosed(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(4 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var b [4]byte
	n, err := conn.Read(b[:])
	if n != 0 || !(errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET)) {
		t.Fatal("MQTT peer did not close without acknowledgement", b, n, err)
	}
}
