//go:build darwin || linux

package oscore

import (
	"sync"

	"github.com/Someblueman/airc/internal/gateway/store"
)

// A replay-window snapshot must have one live native owner. Serial journal
// writes alone cannot prevent two restored processes from decrypting the same
// packet. Canonical digests also reject copies under different owner labels.
var liveContexts = struct {
	sync.Mutex
	keys map[[32]byte]struct{}
}{keys: make(map[[32]byte]struct{})}

func claimContext(digest [32]byte) (func(), error) {
	liveContexts.Lock()
	defer liveContexts.Unlock()
	if _, exists := liveContexts.keys[digest]; exists {
		return nil, store.ErrConflict
	}
	liveContexts.keys[digest] = struct{}{}
	return func() {
		liveContexts.Lock()
		delete(liveContexts.keys, digest)
		liveContexts.Unlock()
	}, nil
}
