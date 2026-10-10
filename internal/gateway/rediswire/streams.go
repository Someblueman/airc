package rediswire

import (
	"bufio"
	"bytes"
	"strconv"
	"strings"
)

// StreamEntry retains a deleted pending entry separately from an empty payload.
// A deleted entry has no request and cannot establish durable intake.
type StreamEntry struct {
	ID      string
	Payload []byte
	Deleted bool
}

// ReadStreamEntry consumes a COUNT 1 XREADGROUP reply for one literal stream.
// Streams require five array levels; ordinary Read keeps its four-level bound.
// Errors are terminal for this reader. The caller owns deadlines and recovery.
func ReadStreamEntry(r *bufio.Reader, stream string) (*StreamEntry, error) {
	if r == nil || stream == "" || len(stream) > 255 {
		return nil, ErrFrame
	}
	d := decoder{r: r, remaining: MaxFrame, maxDepth: 5}
	v, err := d.value(0)
	if err != nil {
		return nil, err
	}
	if v.Type == Array && v.Null {
		return nil, nil
	}
	if !streamArray(v, 1) || !streamArray(v.Array[0], 2) {
		return nil, ErrFrame
	}
	tuple := v.Array[0].Array
	if tuple[0].Type != Bulk || tuple[0].Null || !bytes.Equal(tuple[0].Bytes, []byte(stream)) || tuple[1].Type != Array || tuple[1].Null || len(tuple[1].Array) > 1 {
		return nil, ErrFrame
	}
	if len(tuple[1].Array) == 0 {
		return nil, nil
	}
	e := tuple[1].Array[0]
	if !streamArray(e, 2) || e.Array[0].Type != Bulk || e.Array[0].Null || !StreamEntryID(string(e.Array[0].Bytes)) {
		return nil, ErrFrame
	}
	entry := &StreamEntry{ID: string(e.Array[0].Bytes)}
	fields := e.Array[1]
	if fields.Type == Array && fields.Null {
		entry.Deleted = true
		return entry, nil
	}
	if !streamArray(fields, 2) || fields.Array[0].Type != Bulk || fields.Array[0].Null || string(fields.Array[0].Bytes) != "intent" || fields.Array[1].Type != Bulk || fields.Array[1].Null || len(fields.Array[1].Bytes) == 0 {
		return nil, ErrFrame
	}
	entry.Payload = fields.Array[1].Bytes
	return entry, nil
}

func streamArray(v Value, n int) bool { return v.Type == Array && !v.Null && len(v.Array) == n }

// StreamEntryID accepts canonical nonzero uint64 pairs, never read cursors.
func StreamEntryID(id string) bool {
	a, b, ok := strings.Cut(id, "-")
	if !ok || a == "" || b == "" || len(a) > 20 || len(b) > 20 {
		return false
	}
	for _, part := range []string{a, b} {
		if len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return false
			}
		}
		if _, err := strconv.ParseUint(part, 10, 64); err != nil {
			return false
		}
	}
	return a != "0" || b != "0"
}
