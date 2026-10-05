//go:build darwin || linux

package gateway_test

import (
	"context"
	"encoding/binary"
)

func webRTCResponseFragment(data []byte) (id [16]byte, total, index, count int, payload []byte, err error) {
	if len(data) < 28 || len(data) > 1000 || string(data[:4]) != "AGD1" {
		return id, 0, 0, 0, nil, context.Canceled
	}
	copy(id[:], data[4:20])
	total = int(binary.BigEndian.Uint32(data[20:24]))
	index = int(binary.BigEndian.Uint16(data[24:26]))
	count = int(binary.BigEndian.Uint16(data[26:28]))
	if total < 1 || total > 64<<10 || count < 1 || count != (total+971)/972 || index >= count {
		return id, 0, 0, 0, nil, context.Canceled
	}
	want := 972
	if remaining := total - index*972; remaining < want {
		want = remaining
	}
	if len(data)-28 != want {
		return id, 0, 0, 0, nil, context.Canceled
	}
	return id, total, index, count, data[28:], nil
}
