package adapter

import (
	"encoding/binary"
	"errors"
)

const (
	MaxNDEFBytes   = 2040
	ndefBundleMIME = "application/vnd.airc.bundle+json"
)

var ErrNDEFBundle = errors.New("invalid NDEF offline bundle")

// DecodeNDEFBundle accepts one MIME NDEF record containing the existing
// unsigned offline-bundle container. Authentication remains per envelope.
func DecodeNDEFBundle(data []byte) (OfflineBundle, error) {
	if len(data) < 3 || len(data) > MaxNDEFBytes {
		return OfflineBundle{}, ErrNDEFBundle
	}
	flags := data[0]
	if flags&0xc0 != 0xc0 || flags&0x28 != 0 || flags&0x07 != 0x02 {
		return OfflineBundle{}, ErrNDEFBundle
	}
	offset := 1
	typeLength := int(data[offset])
	offset++
	if flags&0x10 != 0 {
		if offset >= len(data) {
			return OfflineBundle{}, ErrNDEFBundle
		}
		payloadLength := int(data[offset])
		offset++
		return decodeNDEFBundlePayload(data, offset, typeLength, payloadLength)
	}
	if len(data)-offset < 4 {
		return OfflineBundle{}, ErrNDEFBundle
	}
	encodedLength := binary.BigEndian.Uint32(data[offset : offset+4])
	offset += 4
	if encodedLength > MaxNDEFBytes {
		return OfflineBundle{}, ErrNDEFBundle
	}
	return decodeNDEFBundlePayload(data, offset, typeLength, int(encodedLength))
}

func decodeNDEFBundlePayload(data []byte, offset, typeLength, payloadLength int) (OfflineBundle, error) {
	if typeLength != len(ndefBundleMIME) || payloadLength < 1 || payloadLength > MaxNDEFBytes ||
		offset > len(data) || typeLength > len(data)-offset || payloadLength != len(data)-offset-typeLength {
		return OfflineBundle{}, ErrNDEFBundle
	}
	if string(data[offset:offset+typeLength]) != ndefBundleMIME {
		return OfflineBundle{}, ErrNDEFBundle
	}
	start := offset + typeLength
	bundle, err := DecodeOfflineBundle(data[start : start+payloadLength])
	if err != nil {
		return OfflineBundle{}, ErrNDEFBundle
	}
	return bundle, nil
}
