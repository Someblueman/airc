package adapter

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Someblueman/airc/gateway/internal/dispatch"
	"github.com/Someblueman/airc/gateway/internal/store"
)

// Datagram carriers split a frame into fragments of at most
// fragmentDatagramBytes: a 28-byte header (the wire tag "AGD1", a random
// 16-byte message ID, the total length, the fragment index and count) and a
// payload slice. The tag is the framing's fixed wire value.
const (
	fragmentMagic         = "AGD1"
	fragmentHeaderBytes   = 28
	fragmentDatagramBytes = 1000
	fragmentPayloadBytes  = fragmentDatagramBytes - fragmentHeaderBytes
	fragmentMaxPending    = 4
	fragmentReplayLimit   = 64
	fragmentReplayAge     = 30 * time.Second
	fragmentAssemblyAge   = 5 * time.Second
)

var errFragment = errors.New("invalid datagram fragment")

type datagramAssembly struct {
	total     int
	count     int
	fragments [][]byte
	received  int
	deadline  time.Time
}

type datagramAssembler struct {
	pending   map[[16]byte]*datagramAssembly
	completed map[[16]byte]time.Time
}

func newDatagramAssembler() *datagramAssembler {
	return &datagramAssembler{pending: make(map[[16]byte]*datagramAssembly), completed: make(map[[16]byte]time.Time)}
}

// add validates one complete datagram. Invalid or conflicting datagrams are
// dropped before a correlated frame can be dispatched; only a fully assembled
// frame is returned.
func (a *datagramAssembler) add(data []byte, now time.Time) ([]byte, error) {
	a.expire(now)
	id, total, index, count, payload, err := parseFragment(data)
	if err != nil {
		return nil, nil
	}
	if _, done := a.completed[id]; done {
		return nil, nil
	}
	assembly := a.pending[id]
	if assembly == nil {
		if len(a.pending) >= fragmentMaxPending {
			return nil, nil
		}
		assembly = &datagramAssembly{total: total, count: count, fragments: make([][]byte, count), deadline: now.Add(fragmentAssemblyAge)}
		a.pending[id] = assembly
	} else if assembly.total != total || assembly.count != count {
		delete(a.pending, id)
		return nil, nil
	}
	if prior := assembly.fragments[index]; prior != nil {
		if string(prior) != string(payload) {
			delete(a.pending, id)
		}
		return nil, nil
	}
	fragment := append([]byte(nil), payload...)
	assembly.fragments[index] = fragment
	assembly.received++
	if assembly.received != assembly.count {
		return nil, nil
	}
	message := make([]byte, assembly.total)
	offset := 0
	for _, part := range assembly.fragments {
		copy(message[offset:], part)
		offset += len(part)
	}
	delete(a.pending, id)
	a.completed[id] = now.Add(fragmentReplayAge)
	// Enforce the replay bound after insertion as well as during the next
	// receive. A peer can otherwise retain one extra completed ID indefinitely.
	a.expire(now)
	return message, nil
}

func (a *datagramAssembler) expire(now time.Time) {
	for id, assembly := range a.pending {
		if !now.Before(assembly.deadline) {
			delete(a.pending, id)
		}
	}
	for id, deadline := range a.completed {
		if !now.Before(deadline) {
			delete(a.completed, id)
		}
	}
	for len(a.completed) > fragmentReplayLimit {
		var oldest [16]byte
		var oldestAt time.Time
		for id, deadline := range a.completed {
			if oldestAt.IsZero() || deadline.Before(oldestAt) {
				oldest, oldestAt = id, deadline
			}
		}
		delete(a.completed, oldest)
	}
}

func parseFragment(data []byte) (id [16]byte, total, index, count int, payload []byte, err error) {
	if len(data) < fragmentHeaderBytes || len(data) > fragmentDatagramBytes || string(data[:4]) != fragmentMagic {
		return id, 0, 0, 0, nil, errFragment
	}
	copy(id[:], data[4:20])
	total = int(binary.BigEndian.Uint32(data[20:24]))
	index = int(binary.BigEndian.Uint16(data[24:26]))
	count = int(binary.BigEndian.Uint16(data[26:28]))
	if total < 1 || total > MaxRequestFrameBytes || count < 1 || count != (total+fragmentPayloadBytes-1)/fragmentPayloadBytes || index < 0 || index >= count {
		return id, 0, 0, 0, nil, errFragment
	}
	expected := fragmentPayloadBytes
	if remaining := total - index*fragmentPayloadBytes; remaining < expected {
		expected = remaining
	}
	if len(data)-fragmentHeaderBytes != expected {
		return id, 0, 0, 0, nil, errFragment
	}
	return id, total, index, count, data[fragmentHeaderBytes:], nil
}

func splitFragments(message []byte) ([][]byte, error) {
	if len(message) < 1 || len(message) > MaxResponseFrameBytes {
		return nil, errFragment
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("generate fragment message ID: %w", err)
	}
	count := (len(message) + fragmentPayloadBytes - 1) / fragmentPayloadBytes
	fragments := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		start := index * fragmentPayloadBytes
		end := start + fragmentPayloadBytes
		if end > len(message) {
			end = len(message)
		}
		fragment := make([]byte, fragmentHeaderBytes+end-start)
		copy(fragment[:4], fragmentMagic)
		copy(fragment[4:20], id[:])
		binary.BigEndian.PutUint32(fragment[20:24], uint32(len(message)))
		binary.BigEndian.PutUint16(fragment[24:26], uint16(index))
		binary.BigEndian.PutUint16(fragment[26:28], uint16(count))
		copy(fragment[fragmentHeaderBytes:], message[start:end])
		fragments = append(fragments, fragment)
	}
	return fragments, nil
}

// datagramCarrier is the shared unreliable endpoint used by WebTransport and
// raw QUIC. Both transports preserve the fragment wire format and sequencing.
type datagramCarrier interface {
	SendDatagram([]byte) error
	ReceiveDatagram(context.Context) ([]byte, error)
}

// plain, when not nil, admits plain frames run as their author with the
// carrying connection's source and session.
func serveFragmentDatagrams(parent context.Context, carrier datagramCarrier, closeConn func(), d *dispatch.Dispatcher, requests chan struct{}, mode string, authenticatedActivity func(), plain *dispatch.Ingress) error {
	if carrier == nil || closeConn == nil || d == nil || cap(requests) < 1 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var writes sync.Mutex
	write := func(response FrameResponse) error {
		body, err := encodeFrame(response)
		if err != nil || len(body) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		fragments, err := splitFragments(body)
		if err != nil {
			return store.ErrCapacity
		}
		writes.Lock()
		defer writes.Unlock()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		writeCtx, finish := context.WithTimeout(ctx, 3*time.Second)
		closed := make(chan struct{})
		stop := context.AfterFunc(writeCtx, func() {
			closeConn()
			close(closed)
		})
		defer func() {
			if !stop() {
				<-closed
			}
			finish()
		}()
		for _, fragment := range fragments {
			if err := carrier.SendDatagram(fragment); err != nil {
				return err
			}
		}
		if err := writeCtx.Err(); err != nil {
			return err
		}
		// Errors and transport input never extend a credential-bearing lease.
		if authenticatedActivity != nil && (response.Result != nil || response.Page != nil) {
			authenticatedActivity()
		}
		return nil
	}
	assembler := newDatagramAssembler()
	next := func() ([]byte, error) {
		for {
			now := time.Now()
			wake := now.Add(30 * time.Second)
			for _, pending := range assembler.pending {
				if pending.deadline.Before(wake) {
					wake = pending.deadline
				}
			}
			receiveCtx, finish := context.WithDeadline(ctx, wake)
			data, err := carrier.ReceiveDatagram(receiveCtx)
			finish()
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				assembler.expire(time.Now())
				if errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				return nil, err
			}
			body, _ := assembler.add(data, time.Now())
			if body != nil {
				return body, nil
			}
		}
	}
	return runCorrelated(ctx, correlatedTransport{next: next, write: write, sequenced: true, watchModes: []string{"long_poll", mode}, plain: plain}, d, requests)
}
