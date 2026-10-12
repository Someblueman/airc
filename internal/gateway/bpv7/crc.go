package bpv7

import (
	"encoding/binary"
	"hash/crc32"
	"sync"
)

// castagnoli is built on first use: airc links this package for config
// validation, and a package-level table would cost every command at startup.
var castagnoli = sync.OnceValue(func() *crc32.Table { return crc32.MakeTable(crc32.Castagnoli) })

// checkCRC includes the received array encoding and any break byte. Only the
// CRC value bytes are treated as zero; their byte-string header remains covered.
func checkCRC(block []byte, offset int, expected []byte, kind uint64) bool {
	if offset < 0 || offset+len(expected) > len(block) {
		return false
	}
	switch kind {
	case 1:
		if len(expected) != 2 {
			return false
		}
		return crc16X25(block, offset) == binary.BigEndian.Uint16(expected)
	case 2:
		if len(expected) != 4 {
			return false
		}
		crc := crc32.Update(0, castagnoli(), block[:offset])
		crc = crc32.Update(crc, castagnoli(), []byte{0, 0, 0, 0})
		crc = crc32.Update(crc, castagnoli(), block[offset+4:])
		return crc == binary.BigEndian.Uint32(expected)
	default:
		return false
	}
}

func (r *reader) crc(start int, kind uint64, indefinite bool) {
	expected := r.bytes(2, 4)
	offset := r.pos - len(expected) - start
	r.end(indefinite)
	if r.err != nil || !checkCRC(r.data[start:r.pos], offset, expected, kind) {
		r.err = ErrBundle
	}
}

func crc16X25(block []byte, zeroAt int) uint16 {
	crc := uint16(0xffff)
	for i, value := range block {
		if zeroAt >= 0 && i >= zeroAt && i < zeroAt+2 {
			value = 0
		}
		crc ^= uint16(value)
		for range 8 {
			if crc&1 != 0 {
				crc = crc>>1 ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return crc ^ 0xffff
}
