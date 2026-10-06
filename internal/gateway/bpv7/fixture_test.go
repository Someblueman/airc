package bpv7

import (
	"encoding/binary"
	"hash/crc32"
	"math/bits"
)

type wireFixture struct {
	source, destination, payload                                                                    string
	creation, sequence, lifetime, flags, primaryCRC, payloadCRC, blockType, blockNumber, blockFlags uint64
	indefinite, tag                                                                                 bool
}

func defaultWire() wireFixture {
	return wireFixture{source: "dtn://sender/", destination: "dtn://receiver/airc", payload: "opaque.signed.envelope", creation: 1000000, sequence: 17, lifetime: 60000, flags: 4, primaryCRC: 1, payloadCRC: 2, blockType: 1, blockNumber: 1}
}

func wireHead(major byte, value uint64) []byte {
	switch {
	case value < 24:
		return []byte{major<<5 | byte(value)}
	case value <= 255:
		return []byte{major<<5 | 24, byte(value)}
	case value <= 65535:
		return binary.BigEndian.AppendUint16([]byte{major<<5 | 25}, uint16(value))
	case value <= 4294967295:
		return binary.BigEndian.AppendUint32([]byte{major<<5 | 26}, uint32(value))
	default:
		return binary.BigEndian.AppendUint64([]byte{major<<5 | 27}, value)
	}
}

func wireEID(eid string, indefinite bool) []byte {
	var out []byte
	if indefinite {
		out = []byte{0x9f, 1}
	} else {
		out = []byte{0x82, 1}
	}
	if eid == "dtn:none" {
		out = append(out, 0)
	} else {
		ssp := []byte(eid[4:])
		out = append(out, wireHead(3, uint64(len(ssp)))...)
		out = append(out, ssp...)
	}
	if indefinite {
		out = append(out, 0xff)
	}
	return out
}

func referenceCRC16(data []byte) uint16 {
	crc := uint16(0xffff)
	for _, value := range data {
		crc ^= uint16(bits.Reverse8(value)) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return bits.Reverse16(crc) ^ 0xffff
}

func finishWireBlock(block []byte, kind uint64, indefinite bool) []byte {
	if kind == 0 {
		if indefinite {
			return append(block, 0xff)
		}
		return block
	}
	size := 2
	if kind != 1 {
		size = 4
	}
	block = append(block, byte(0x40+size))
	offset := len(block)
	block = append(block, make([]byte, size)...)
	if indefinite {
		block = append(block, 0xff)
	}
	if kind == 1 {
		binary.BigEndian.PutUint16(block[offset:], referenceCRC16(block))
	} else {
		binary.BigEndian.PutUint32(block[offset:], crc32.Checksum(block, crc32.MakeTable(crc32.Castagnoli)))
	}
	return block
}

func (f wireFixture) bytes() []byte {
	primary := []byte{0x89}
	if f.primaryCRC == 0 {
		primary[0] = 0x88
	}
	if f.indefinite {
		primary[0] = 0x9f
	}
	for _, value := range []uint64{7, f.flags, f.primaryCRC} {
		primary = append(primary, wireHead(0, value)...)
	}
	primary = append(primary, wireEID(f.destination, f.indefinite)...)
	primary = append(primary, wireEID(f.source, f.indefinite)...)
	primary = append(primary, wireEID("dtn:none", f.indefinite)...)
	if f.indefinite {
		primary = append(primary, 0x9f)
	} else {
		primary = append(primary, 0x82)
	}
	primary = append(primary, wireHead(0, f.creation)...)
	primary = append(primary, wireHead(0, f.sequence)...)
	if f.indefinite {
		primary = append(primary, 0xff)
	}
	primary = append(primary, wireHead(0, f.lifetime)...)
	primary = finishWireBlock(primary, f.primaryCRC, f.indefinite)
	payload := []byte{0x86}
	if f.payloadCRC == 0 {
		payload[0] = 0x85
	}
	if f.indefinite {
		payload[0] = 0x9f
	}
	for _, value := range []uint64{f.blockType, f.blockNumber, f.blockFlags, f.payloadCRC} {
		payload = append(payload, wireHead(0, value)...)
	}
	payload = append(payload, wireHead(2, uint64(len(f.payload)))...)
	payload = append(payload, f.payload...)
	payload = finishWireBlock(payload, f.payloadCRC, f.indefinite)
	out := []byte{0x9f}
	if f.tag {
		out = []byte{0xd9, 0xd9, 0xf7, 0x9f}
	}
	out = append(out, primary...)
	out = append(out, payload...)
	return append(out, 0xff)
}
