package sftpwire

import (
	"encoding/binary"
	"errors"
	"io"
)

const maxPacketBytes = 64 << 10

const (
	packetInit    = 1
	packetVersion = 2
	packetOpen    = 3
	packetClose   = 4
	packetRead    = 5
	packetWrite   = 6
	packetLstat   = 7
	packetFstat   = 8
	packetRename  = 18
	packetStatus  = 101
	packetHandle  = 102
	packetData    = 103
	packetAttrs   = 105
	packetFsync   = 200
)

var errPacket = errors.New("invalid bounded SFTP packet")

type encoder []byte

func (e *encoder) u32(v uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	*e = append(*e, b[:]...)
}

func (e *encoder) u64(v uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	*e = append(*e, b[:]...)
}

func (e *encoder) string(v []byte) {
	e.u32(uint32(len(v)))
	*e = append(*e, v...)
}

func (e *encoder) text(v string) { e.string([]byte(v)) }

type decoder struct{ data []byte }

func (d *decoder) u32() (uint32, error) {
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (d *decoder) u64() (uint64, error) {
	b, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(b), nil
}

func (d *decoder) string(max int) ([]byte, error) {
	n, err := d.u32()
	if err != nil || uint64(n) > uint64(max) {
		return nil, errPacket
	}
	return d.take(int(n))
}

func (d *decoder) take(n int) ([]byte, error) {
	if n < 0 || n > len(d.data) {
		return nil, errPacket
	}
	b := d.data[:n]
	d.data = d.data[n:]
	return b, nil
}

func (d *decoder) done() error {
	if len(d.data) != 0 {
		return errPacket
	}
	return nil
}

func writePacket(w io.Writer, typ byte, payload []byte) error {
	length := len(payload) + 1
	if length < 1 || length > maxPacketBytes {
		return errPacket
	}
	var header [5]byte
	binary.BigEndian.PutUint32(header[:4], uint32(length))
	header[4] = typ
	if err := writeAll(w, header[:]); err != nil {
		return err
	}
	return writeAll(w, payload)
}

func readPacket(r io.Reader) (byte, []byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, nil, err
	}
	length := binary.BigEndian.Uint32(header[:])
	if length < 1 || length > maxPacketBytes {
		return 0, nil, errPacket
	}
	body := make([]byte, int(length))
	if _, err := io.ReadFull(r, body); err != nil {
		clear(body)
		return 0, nil, err
	}
	return body[0], body[1:], nil
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(p) {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
