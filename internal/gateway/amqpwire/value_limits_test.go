package amqpwire

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestValueTotalNodeBudget(t *testing.T) {
	list := func(elements []byte, count uint32) []byte {
		h := make([]byte, 9)
		h[0] = 0xd0
		binary.BigEndian.PutUint32(h[1:5], uint32(len(elements))+4)
		binary.BigEndian.PutUint32(h[5:9], count)
		return append(h, elements...)
	}
	for _, leaves := range []int{1023, 1024} {
		first := list(bytes.Repeat([]byte{0x40}, leaves), uint32(leaves))
		second := list(bytes.Repeat([]byte{0x40}, 1022), 1022)
		wire := list(append(first, second...), 2)
		_, _, err := DecodeValue(wire)
		// Root and two child lists plus 2045 leaves exactly consume 2048 nodes.
		if (leaves == 1023) != (err == nil) {
			t.Fatalf("leaves=%d err=%v", leaves, err)
		}
	}
}
