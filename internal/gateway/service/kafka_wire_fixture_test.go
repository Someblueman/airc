//go:build darwin || linux

package service

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/kafkawire"
)

func kafkaServiceRequest(t *testing.T, r io.Reader) (int16, int16, int32, []byte, error) {
	t.Helper()
	var prefix [4]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		return 0, 0, 0, nil, err
	}
	size := binary.BigEndian.Uint32(prefix[:])
	if size < 22 || size > kafkawire.MaxFrame {
		t.Error("invalid request size")
		return 0, 0, 0, nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, 0, 0, nil, err
	}
	if binary.BigEndian.Uint16(body[8:]) != 12 || string(body[10:22]) != kafkawire.ClientID {
		t.Error("client header")
	}
	return int16(binary.BigEndian.Uint16(body)), int16(binary.BigEndian.Uint16(body[2:])), int32(binary.BigEndian.Uint32(body[4:])), body[22:], nil
}
func kafkaServiceReply(w io.Writer, correlation int32, body []byte) error {
	wire := binary.BigEndian.AppendUint32(nil, uint32(len(body)+4))
	wire = binary.BigEndian.AppendUint32(wire, uint32(correlation))
	wire = append(wire, body...)
	_, err := w.Write(wire)
	return err
}
func kafkaServiceAuth(t *testing.T, rw io.ReadWriter) bool {
	t.Helper()
	versions := []byte{0, 0, 0, 0, 0, 8}
	for _, v := range [][2]uint16{{18, 0}, {17, 1}, {36, 1}, {3, 8}, {2, 5}, {1, 11}, {0, 7}, {23, 3}} {
		for _, n := range []uint16{v[0], v[1], v[1]} {
			versions = binary.BigEndian.AppendUint16(versions, n)
		}
	}
	plain, _ := kafkawire.PlainAuthenticate("user", []byte("password"))
	defer clear(plain)
	for _, step := range []struct {
		api, version   int16
		request, reply []byte
	}{
		{18, 0, nil, versions}, {17, 1, kafkawire.PlainHandshake(), []byte{0, 0, 0, 0, 0, 1, 0, 5, 'P', 'L', 'A', 'I', 'N'}}, {36, 1, plain, []byte{0, 0, 255, 255, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}},
	} {
		api, v, corr, body, err := kafkaServiceRequest(t, rw)
		if err != nil || api != step.api || v != step.version || !bytes.Equal(body, step.request) {
			t.Error("setup sequence", err)
			return false
		}
		if kafkaServiceReply(rw, corr, step.reply) != nil {
			return false
		}
	}
	return true
}

func kafkaServiceFetchReply(topic string, partition int32, batch []byte) []byte {
	b := binary.BigEndian.AppendUint32(nil, 0)
	b = binary.BigEndian.AppendUint16(b, 0)
	for _, n := range []uint32{0, 1} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(topic)))
	b = append(b, topic...)
	for _, n := range []uint32{1, uint32(partition)} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = binary.BigEndian.AppendUint16(b, 0)
	for _, n := range []uint64{40, 40, 0} {
		b = binary.BigEndian.AppendUint64(b, n)
	}
	for _, n := range []uint32{0xffffffff, 0xffffffff, uint32(len(batch))} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	return append(b, batch...)
}
func kafkaServiceProduceReply(topic string, partition int32) []byte {
	b := binary.BigEndian.AppendUint32(nil, 1)
	b = binary.BigEndian.AppendUint16(b, uint16(len(topic)))
	b = append(b, topic...)
	for _, n := range []uint32{1, uint32(partition)} {
		b = binary.BigEndian.AppendUint32(b, n)
	}
	b = binary.BigEndian.AppendUint16(b, 0)
	for _, n := range []uint64{100, 0xffffffffffffffff, 0} {
		b = binary.BigEndian.AppendUint64(b, n)
	}
	return binary.BigEndian.AppendUint32(b, 0)
}
