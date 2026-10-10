package bluezgatt

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/config"
)

const (
	maxSessionPeers   = 16
	maxRequestBytes   = 16 << 10
	maxResultBytes    = 64 << 10
	maxCharacteristic = 512
	maxBLEMTU         = 517
	partialLifetime   = 30 * time.Second
	resultLifetime    = 60 * time.Second
	commitTimeout     = 4 * time.Second
	responseHeaderLen = 17
)

var (
	ErrSessionClosed = errors.New("ble gatt session is closed")
	ErrPeerLimit     = errors.New("ble gatt peer limit reached")
	ErrSessionBusy   = errors.New("ble gatt peer already has an active transfer")
	ErrInvalidValue  = errors.New("invalid ble gatt transfer value")
	ErrNoResult      = errors.New("ble gatt result is unavailable")
	ErrNoCapacity    = errors.New("ble gatt transfer capacity is exhausted")
)

type transferPhase uint8

const (
	phasePartial transferPhase = iota + 1
	phaseCommitting
	phaseResult
)

type peerTransfer struct {
	phase        transferPhase
	id           [8]byte
	request      []byte
	requestLen   uint16
	result       []byte
	readOffset   uint32
	expiresAt    time.Time
	cancel       context.CancelFunc
	expired      bool
	reservedSlot bool
}

// Sessions owns bounded upload and protected-result state for configured BLE
// peers. slots is shared with the owning service and is never closed here.
type Sessions struct {
	mu        sync.Mutex
	closed    bool
	peers     map[Peer]*peerTransfer
	maxPeers  int
	slots     chan struct{}
	handler   Handler
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// NewSessions creates a bounded per-device session manager. The shared slots
// channel reserves capacity from the first request chunk through result reset
// or expiry.
func NewSessions(maxPeers int, slots chan struct{}, handler Handler) (*Sessions, error) {
	if maxPeers < 1 || maxPeers > maxSessionPeers || slots == nil || cap(slots) < 1 || handler == nil {
		return nil, errors.New("invalid ble gatt session limits or handler")
	}
	return &Sessions{
		peers:    make(map[Peer]*peerTransfer, maxPeers),
		maxPeers: maxPeers,
		slots:    slots,
		handler:  handler,
	}, nil
}

// Write applies one application-framed characteristic write. The object layer
// must first authenticate the BlueZ caller and validate the device, LE link,
// paired state, WriteValue type, ATT offset, and MTU.
func (s *Sessions) Write(ctx context.Context, peer Peer, mtu uint16, value []byte) error {
	if s == nil || ctx == nil {
		return ErrSessionClosed
	}
	if err := validateSessionPeer(peer); err != nil {
		return err
	}
	if mtu < 23 || mtu > maxBLEMTU || len(value) == 0 || len(value) > min(maxCharacteristic, int(mtu)-3) {
		return ErrInvalidValue
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrSessionClosed
	}
	state := s.peers[peer]
	if state != nil && !state.expiresAt.After(time.Now()) && state.phase != phaseCommitting {
		s.removeLocked(peer, state)
		state = nil
	}
	if len(value) < 2 || value[0] != 1 {
		s.mu.Unlock()
		return ErrInvalidValue
	}
	var err error
	var commit *commitWork
	switch value[1] {
	case 0:
		err = s.writeChunkLocked(peer, state, value)
	case 1:
		commit, err = s.beginCommitLocked(ctx, peer, state, value)
	case 2:
		err = s.selectResultLocked(peer, state, value)
	case 3:
		err = s.resetLocked(peer, state, value)
	default:
		err = ErrInvalidValue
	}
	s.mu.Unlock()
	if commit != nil {
		return s.finishCommit(peer, commit)
	}
	return err
}

func (s *Sessions) writeChunkLocked(peer Peer, state *peerTransfer, value []byte) error {
	if len(value) <= 12 || len(value) > maxCharacteristic {
		return ErrInvalidValue
	}
	var id [8]byte
	copy(id[:], value[2:10])
	if id == ([8]byte{}) {
		return ErrInvalidValue
	}
	offset := binary.BigEndian.Uint16(value[10:12])
	data := value[12:]
	if len(data) == 0 {
		return ErrInvalidValue
	}
	if state == nil {
		if offset != 0 {
			return ErrInvalidValue
		}
		if len(s.peers) >= s.maxPeers {
			return ErrPeerLimit
		}
		select {
		case s.slots <- struct{}{}:
		default:
			return ErrNoCapacity
		}
		state = &peerTransfer{phase: phasePartial, id: id, expiresAt: time.Now().Add(partialLifetime), reservedSlot: true}
		s.peers[peer] = state
	} else if state.phase != phasePartial {
		return ErrSessionBusy
	} else if state.id != id || int(offset) != len(state.request) {
		s.removeLocked(peer, state)
		return ErrInvalidValue
	}
	if int(offset)+len(data) > maxRequestBytes {
		s.removeLocked(peer, state)
		return ErrInvalidValue
	}
	state.request = append(state.request, data...)
	return nil
}

type commitWork struct {
	state   *peerTransfer
	id      [8]byte
	ctx     context.Context
	cancel  context.CancelFunc
	request []byte
}

func (s *Sessions) beginCommitLocked(ctx context.Context, peer Peer, state *peerTransfer, value []byte) (*commitWork, error) {
	if len(value) != 12 {
		return nil, ErrInvalidValue
	}
	var id [8]byte
	copy(id[:], value[2:10])
	total := binary.BigEndian.Uint16(value[10:12])
	if id == ([8]byte{}) || total == 0 || total > maxRequestBytes {
		return nil, ErrInvalidValue
	}
	if state == nil || state.id != id {
		if state != nil && state.phase == phasePartial {
			s.removeLocked(peer, state)
		}
		return nil, ErrNoResult
	}
	if state.phase == phaseResult {
		if state.requestLen == total {
			return nil, nil
		}
		return nil, ErrInvalidValue
	}
	if state.phase != phasePartial {
		return nil, ErrSessionBusy
	}
	if len(state.request) != int(total) {
		s.removeLocked(peer, state)
		return nil, ErrInvalidValue
	}
	request := bytes.Clone(state.request)
	state.requestLen = total
	state.phase = phaseCommitting
	commitCtx, cancel := context.WithTimeout(ctx, commitTimeout)
	state.cancel = cancel
	s.wg.Add(1)
	return &commitWork{state: state, id: id, ctx: commitCtx, cancel: cancel, request: request}, nil
}

func (s *Sessions) finishCommit(peer Peer, work *commitWork) error {
	defer s.wg.Done()
	defer work.cancel()
	defer clear(work.request)
	result, err := s.handler(work.ctx, peer.Address, work.id, work.request)
	if err == nil && work.ctx.Err() != nil {
		err = work.ctx.Err()
	}
	if len(result) > maxResultBytes {
		clear(result)
		err = ErrInvalidValue
	}
	s.mu.Lock()
	state := work.state
	if s.closed || s.peers[peer] != state || state.expired {
		clear(result)
		if s.peers[peer] == state {
			s.removeLocked(peer, state)
		}
		s.mu.Unlock()
		return ErrSessionClosed
	}
	state.cancel = nil
	if err != nil || len(result) == 0 {
		clear(result)
		if err == nil {
			err = ErrInvalidValue
		}
		s.removeLocked(peer, state)
		s.mu.Unlock()
		return err
	}
	state.result = bytes.Clone(result)
	clear(result)
	clear(state.request)
	state.request = nil
	state.phase = phaseResult
	state.readOffset = 0
	state.expiresAt = time.Now().Add(resultLifetime)
	s.mu.Unlock()
	return nil
}

// Read returns a stable encrypted result page for one peer. offset is the ATT
// ReadValue offset and must remain zero; result paging uses SelectResponse.
func (s *Sessions) Read(peer Peer, mtu uint16, offset uint16) ([]byte, error) {
	if s == nil || validateSessionPeer(peer) != nil {
		return nil, ErrNoResult
	}
	if offset != 0 || mtu < 23 || mtu > maxBLEMTU {
		return nil, ErrInvalidValue
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	state := s.peers[peer]
	if state == nil || state.phase != phaseResult {
		return nil, ErrNoResult
	}
	if !state.expiresAt.After(time.Now()) {
		s.removeLocked(peer, state)
		return nil, ErrNoResult
	}
	if state.readOffset > uint32(len(state.result)) {
		return nil, ErrNoResult
	}
	maxValue := min(maxCharacteristic, int(mtu)-1)
	page := make([]byte, responseHeaderLen, maxValue)
	page[0] = 1
	copy(page[1:9], state.id[:])
	binary.BigEndian.PutUint32(page[9:13], uint32(len(state.result)))
	binary.BigEndian.PutUint32(page[13:17], state.readOffset)
	remaining := state.result[state.readOffset:]
	if bodyLen := min(len(remaining), maxValue-responseHeaderLen); bodyLen > 0 {
		page = append(page, remaining[:bodyLen]...)
	}
	return page, nil
}

func (s *Sessions) selectResultLocked(peer Peer, state *peerTransfer, value []byte) error {
	if len(value) != 14 {
		return ErrInvalidValue
	}
	var id [8]byte
	copy(id[:], value[2:10])
	offset := binary.BigEndian.Uint32(value[10:14])
	if id == ([8]byte{}) || state == nil || state.phase != phaseResult || state.id != id || offset > uint32(len(state.result)) {
		return ErrNoResult
	}
	state.readOffset = offset
	return nil
}

func (s *Sessions) resetLocked(peer Peer, state *peerTransfer, value []byte) error {
	if len(value) != 10 {
		return ErrInvalidValue
	}
	var id [8]byte
	copy(id[:], value[2:10])
	if id == ([8]byte{}) || state == nil || state.id != id {
		return ErrNoResult
	}
	if state.phase == phaseCommitting {
		return ErrSessionBusy
	}
	s.removeLocked(peer, state)
	return nil
}

// Reap releases partial and result sessions whose fixed lifetime has elapsed.
// In-flight handlers are cancelled and cleaned up by their owner on return.
func (s *Sessions) Reap(now time.Time) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	for peer, state := range s.peers {
		if state.expiresAt.After(now) {
			continue
		}
		if state.phase == phaseCommitting {
			state.expired = true
			if state.cancel != nil {
				state.cancel()
			}
			continue
		}
		s.removeLocked(peer, state)
	}
}

// Close cancels and joins all admitted handlers, then clears retained request
// and result bytes and releases every shared capacity slot.
func (s *Sessions) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for _, state := range s.peers {
			if state.cancel != nil {
				state.cancel()
			}
		}
		s.mu.Unlock()
		s.wg.Wait()
		s.mu.Lock()
		for peer, state := range s.peers {
			s.removeLocked(peer, state)
		}
		s.mu.Unlock()
	})
	return nil
}

func (s *Sessions) removeLocked(peer Peer, state *peerTransfer) {
	if s.peers[peer] != state {
		return
	}
	delete(s.peers, peer)
	clear(state.request)
	clear(state.result)
	state.request = nil
	state.result = nil
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	if state.reservedSlot {
		<-s.slots
		state.reservedSlot = false
	}
}

func validateSessionPeer(peer Peer) error {
	if _, err := config.ParseBluetoothAddress(peer.Address); err != nil {
		return fmt.Errorf("invalid ble gatt peer address: %w", err)
	}
	if peer.AddressType != "public" && peer.AddressType != "random" {
		return ErrInvalidValue
	}
	return nil
}
