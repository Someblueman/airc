package adapter

import (
	"encoding/hex"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/gateway/store"
	"github.com/plgd-dev/go-coap/v3/message"
	"github.com/plgd-dev/go-coap/v3/message/pool"
	"github.com/plgd-dev/go-coap/v3/udp/client"
	"github.com/plgd-dev/go-coap/v3/udp/coder"
)

type blockReservation struct {
	expires    time.Time
	nextOffset int64
}

type coapLedger struct {
	mu      sync.Mutex
	mids    map[int32]struct{}
	tokens  map[string]struct{}
	blocks  map[string]blockReservation
	onReset func(mid int32)
}

func newCoAPLedger(onReset func(int32)) *coapLedger {
	return &coapLedger{mids: make(map[int32]struct{}), tokens: make(map[string]struct{}), blocks: make(map[string]blockReservation), onReset: onReset}
}

// monitor executes before go-coap's queue and Blockwise caches. It retains
// lifetime identities for the association; only incomplete Block1 assemblies
// expire because their library retention is fixed at three seconds.
func (l *coapLedger) monitor(_ *client.Conn, req *pool.Message) (bool, error) {
	if l == nil || req == nil {
		return true, store.ErrInvalid
	}
	now := time.Now()
	var reset func(int32)
	var resetMID int32
	l.mu.Lock()
	for token, reservation := range l.blocks {
		if !now.Before(reservation.expires) {
			delete(l.blocks, token)
		}
	}
	mid := req.MessageID()
	if mid >= 0 {
		if _, known := l.mids[mid]; !known {
			if len(l.mids) >= coapMaxMIDs {
				l.mu.Unlock()
				return true, store.ErrCapacity
			}
			l.mids[mid] = struct{}{}
		}
	}
	rawToken := req.Token()
	tokenKey := ""
	if len(rawToken) > 0 {
		tokenKey = hex.EncodeToString(rawToken)
	}
	if tokenKey != "" {
		if _, known := l.tokens[tokenKey]; !known {
			if len(l.tokens) >= coapMaxTokens {
				l.mu.Unlock()
				return true, store.ErrCapacity
			}
			l.tokens[tokenKey] = struct{}{}
		}
	}
	if req.Type() == message.Reset {
		reset, resetMID = l.onReset, mid
	}
	if req.HasOption(message.Block1) {
		block, err := req.GetOptionUint32(message.Block1)
		if err != nil || block > 0xffffff || blockwiseSZX(block) > 6 {
			l.mu.Unlock()
			return true, store.ErrInvalid
		}
		blockSize := int64(16 << blockwiseSZX(block))
		size, err := req.GetOptionUint32(message.Size1)
		if err == nil && size > coapMaxRequest {
			l.mu.Unlock()
			return true, store.ErrCapacity
		}
		bodySize, err := req.BodySize()
		if err != nil {
			l.mu.Unlock()
			return true, store.ErrInvalid
		}
		if bodySize > blockSize || (block&8 != 0 && bodySize != blockSize) {
			l.mu.Unlock()
			return true, store.ErrCapacity
		}
		offset := int64(block>>4) * blockSize
		if offset >= coapMaxRequest || offset+bodySize > coapMaxRequest {
			l.mu.Unlock()
			return true, store.ErrCapacity
		}
		if tokenKey == "" {
			l.mu.Unlock()
			return true, store.ErrInvalid
		}
		blockMore := block&8 != 0
		reservation, exists := l.blocks[tokenKey]
		if !exists {
			// A one-block M=0 request is forwarded directly by go-coap and
			// never creates a receiving cache entry.
			if blockMore {
				if len(l.blocks) >= coapMaxBlocks {
					l.mu.Unlock()
					return true, store.ErrCapacity
				}
				next := int64(0)
				if offset == 0 {
					next = bodySize
				}
				l.blocks[tokenKey] = blockReservation{expires: now.Add(3 * time.Second), nextOffset: next}
			}
		} else if offset == reservation.nextOffset {
			if blockMore {
				reservation.nextOffset += bodySize
				l.blocks[tokenKey] = reservation
			} else {
				delete(l.blocks, tokenKey)
			}
		}
	}
	l.mu.Unlock()
	if reset != nil {
		reset(resetMID)
	}
	return false, nil
}

func blockwiseSZX(value uint32) uint32 { return value & 7 }

type refusingCache struct {
	mu  sync.Mutex
	max int
	m   map[string][]byte
	exp map[string]time.Time
}

func newRefusingCache(max int) *refusingCache {
	return &refusingCache{max: max, m: make(map[string][]byte), exp: make(map[string]time.Time)}
}
func (c *refusingCache) Load(key string, dst *pool.Message) (bool, error) {
	c.mu.Lock()
	raw, ok := c.m[key]
	expiry := c.exp[key]
	c.mu.Unlock()
	if !ok || time.Now().After(expiry) {
		return false, nil
	}
	_, err := dst.UnmarshalWithDecoder(coder.DefaultCoder, raw)
	return err == nil, err
}
func (c *refusingCache) Store(key string, msg *pool.Message) error {
	raw, err := msg.MarshalWithEncoder(coder.DefaultCoder)
	if err != nil {
		return err
	}
	if len(raw) > coapMTU {
		return store.ErrCapacity
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.m[key]; exists {
		return nil
	}
	if len(c.m) >= c.max {
		return store.ErrCapacity
	}
	c.m[key] = append([]byte(nil), raw...)
	c.exp[key] = time.Now().Add(client.ExchangeLifetime)
	return nil
}
func (c *refusingCache) CheckExpirations(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, expiry := range c.exp {
		if !now.Before(expiry) {
			delete(c.exp, key)
			delete(c.m, key)
		}
	}
}
