package adapter

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"io"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func kafkaPeerRequest(r *bufio.Reader) (int16, int16, int32, []byte, error) {
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return 0, 0, 0, nil, err
	}
	size := binary.BigEndian.Uint32(prefix[:])
	if size < 22 || size > 1<<20 {
		return 0, 0, 0, nil, io.ErrUnexpectedEOF
	}
	b := make([]byte, size)
	if _, err := io.ReadFull(r, b); err != nil {
		return 0, 0, 0, nil, err
	}
	if binary.BigEndian.Uint16(b[8:]) != 12 || string(b[10:22]) != "airc-gateway" {
		return 0, 0, 0, nil, io.ErrUnexpectedEOF
	}
	return int16(binary.BigEndian.Uint16(b)), int16(binary.BigEndian.Uint16(b[2:])), int32(binary.BigEndian.Uint32(b[4:])), b[22:], nil
}
func kafkaPeerReply(c *tls.Conn, correlation int32, body []byte) error {
	b := binary.BigEndian.AppendUint32(nil, uint32(4+len(body)))
	b = binary.BigEndian.AppendUint32(b, uint32(correlation))
	b = append(b, body...)
	_, err := c.Write(b)
	return err
}
func kafkaPeerAuth(t *testing.T, c *tls.Conn, r *bufio.Reader, mode string, lifetime int64) bool {
	t.Helper()
	versions := []byte{0, 0, 0, 0, 0, 8}
	for _, entry := range []struct{ api, version uint16 }{{18, 0}, {17, 1}, {36, 1}, {3, 8}, {2, 5}, {1, 11}, {0, 7}, {23, 3}} {
		for _, n := range []uint16{entry.api, entry.version, entry.version} {
			versions = binary.BigEndian.AppendUint16(versions, n)
		}
	}
	auth := []byte{0, 0, 255, 255, 0, 0, 0, 0}
	auth = binary.BigEndian.AppendUint64(auth, uint64(lifetime))
	for i, step := range []struct {
		api, version   int16
		request, reply []byte
	}{{18, 0, nil, versions}, {17, 1, []byte{0, 5, 'P', 'L', 'A', 'I', 'N'}, []byte{0, 0, 0, 0, 0, 1, 0, 5, 'P', 'L', 'A', 'I', 'N'}}, {36, 1, []byte{0, 0, 0, 14, 0, 'u', 's', 'e', 'r', 0, 'p', 'a', 's', 's', 'w', 'o', 'r', 'd'}, auth}} {
		api, version, correlation, body, err := kafkaPeerRequest(r)
		if err != nil {
			return false
		}
		if api != step.api || version != step.version || correlation != int32(i) || !bytes.Equal(body, step.request) {
			t.Error("incorrect Kafka setup sequence")
			return false
		}
		if mode == "versions" && i == 0 {
			step.reply[0] = 1
		}
		if mode == "handshake" && i == 1 {
			step.reply[0] = 1
		}
		if mode == "auth" && i == 2 {
			step.reply[0] = 1
		}
		if mode == "correlation" && i == 0 {
			correlation++
		}
		if kafkaPeerReply(c, correlation, step.reply) != nil {
			return false
		}
		if mode != "" && ((mode == "versions" || mode == "correlation") && i == 0 || mode == "handshake" && i == 1 || mode == "auth" && i == 2) {
			return false
		}
	}
	return true
}
func kafkaFixtureBroker(b config.RedisPubSub) config.KafkaBroker {
	return config.KafkaBroker{ID: 0, Address: b.Address, TLSServerName: b.TLSServerName}
}
