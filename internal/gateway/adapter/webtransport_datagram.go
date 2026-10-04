package adapter

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/dispatch"
	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/quic-go/quic-go"
	webtransport "github.com/quic-go/webtransport-go"
)

const (
	webTransportDatagramMagic       = "AGD1"
	webTransportDatagramHeaderBytes = 28
	webTransportDatagramBytes       = 1000
	webTransportDatagramPayload     = webTransportDatagramBytes - webTransportDatagramHeaderBytes
	webTransportDatagramMaxPending  = 4
	webTransportDatagramReplayLimit = 64
	webTransportDatagramReplayAge   = 30 * time.Second
	webTransportDatagramAssemblyAge = 5 * time.Second
)

var errWebTransportDatagram = errors.New("invalid webtransport datagram")

type webTransportDatagramAssembly struct {
	total     int
	count     int
	fragments [][]byte
	received  int
	deadline  time.Time
}

type webTransportDatagramAssembler struct {
	pending   map[[16]byte]*webTransportDatagramAssembly
	completed map[[16]byte]time.Time
}

func newWebTransportDatagramAssembler() *webTransportDatagramAssembler {
	return &webTransportDatagramAssembler{pending: make(map[[16]byte]*webTransportDatagramAssembly), completed: make(map[[16]byte]time.Time)}
}

// add validates one complete datagram. Invalid or conflicting datagrams are
// dropped before a correlated frame can be dispatched; only a fully assembled
// frame is returned.
func (a *webTransportDatagramAssembler) add(data []byte, now time.Time) ([]byte, error) {
	a.expire(now)
	id, total, index, count, payload, err := parseWebTransportDatagram(data)
	if err != nil {
		return nil, nil
	}
	if _, done := a.completed[id]; done {
		return nil, nil
	}
	assembly := a.pending[id]
	if assembly == nil {
		if len(a.pending) >= webTransportDatagramMaxPending {
			return nil, nil
		}
		assembly = &webTransportDatagramAssembly{total: total, count: count, fragments: make([][]byte, count), deadline: now.Add(webTransportDatagramAssemblyAge)}
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
	a.completed[id] = now.Add(webTransportDatagramReplayAge)
	// Enforce the replay bound after insertion as well as during the next
	// receive. A peer can otherwise retain one extra completed ID indefinitely.
	a.expire(now)
	return message, nil
}

func (a *webTransportDatagramAssembler) expire(now time.Time) {
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
	for len(a.completed) > webTransportDatagramReplayLimit {
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

func parseWebTransportDatagram(data []byte) (id [16]byte, total, index, count int, payload []byte, err error) {
	if len(data) < webTransportDatagramHeaderBytes || len(data) > webTransportDatagramBytes || string(data[:4]) != webTransportDatagramMagic {
		return id, 0, 0, 0, nil, errWebTransportDatagram
	}
	copy(id[:], data[4:20])
	total = int(binary.BigEndian.Uint32(data[20:24]))
	index = int(binary.BigEndian.Uint16(data[24:26]))
	count = int(binary.BigEndian.Uint16(data[26:28]))
	if total < 1 || total > MaxRequestFrameBytes || count < 1 || count != (total+webTransportDatagramPayload-1)/webTransportDatagramPayload || index < 0 || index >= count {
		return id, 0, 0, 0, nil, errWebTransportDatagram
	}
	expected := webTransportDatagramPayload
	if remaining := total - index*webTransportDatagramPayload; remaining < expected {
		expected = remaining
	}
	if len(data)-webTransportDatagramHeaderBytes != expected {
		return id, 0, 0, 0, nil, errWebTransportDatagram
	}
	return id, total, index, count, data[webTransportDatagramHeaderBytes:], nil
}

func fragmentWebTransportDatagram(message []byte) ([][]byte, error) {
	if len(message) < 1 || len(message) > MaxResponseFrameBytes {
		return nil, errWebTransportDatagram
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("generate webtransport datagram ID: %w", err)
	}
	count := (len(message) + webTransportDatagramPayload - 1) / webTransportDatagramPayload
	fragments := make([][]byte, 0, count)
	for index := 0; index < count; index++ {
		start := index * webTransportDatagramPayload
		end := start + webTransportDatagramPayload
		if end > len(message) {
			end = len(message)
		}
		fragment := make([]byte, webTransportDatagramHeaderBytes+end-start)
		copy(fragment[:4], webTransportDatagramMagic)
		copy(fragment[4:20], id[:])
		binary.BigEndian.PutUint32(fragment[20:24], uint32(len(message)))
		binary.BigEndian.PutUint16(fragment[24:26], uint16(index))
		binary.BigEndian.PutUint16(fragment[26:28], uint16(count))
		copy(fragment[webTransportDatagramHeaderBytes:], message[start:end])
		fragments = append(fragments, fragment)
	}
	return fragments, nil
}

// serveWebTransportDatagrams owns the datagram channel of a session. It
// feeds complete FrameRequest bodies into the common correlated dispatcher and
// fragments each FrameResponse into fresh, independently identified datagrams.
func serveWebTransportDatagrams(parent context.Context, session *webtransport.Session, conn *quic.Conn, d *dispatch.Dispatcher, requests chan struct{}) error {
	if session == nil || conn == nil || d == nil || cap(requests) < 1 {
		return store.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var writes sync.Mutex
	write := func(response FrameResponse) error {
		body, err := json.Marshal(response)
		if err != nil || len(body) > MaxResponseFrameBytes {
			return store.ErrCapacity
		}
		fragments, err := fragmentWebTransportDatagram(body)
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
		stop := context.AfterFunc(writeCtx, func() { _ = conn.CloseWithError(0, "webtransport datagram write bound"); close(closed) })
		defer func() {
			if !stop() {
				<-closed
			}
			finish()
		}()
		for _, fragment := range fragments {
			if err := session.SendDatagram(fragment); err != nil {
				return err
			}
		}
		return writeCtx.Err()

	}

	assembler := newWebTransportDatagramAssembler()
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
			data, err := session.ReceiveDatagram(receiveCtx)
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
	return runCorrelated(ctx, correlatedTransport{next: next, write: write, sequenced: true, watchModes: []string{"long_poll", "webtransport_datagrams"}}, d, requests, false)
}
