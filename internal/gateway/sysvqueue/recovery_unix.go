//go:build (darwin || linux) && arm64

package sysvqueue

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/ipcepoch"
)

// Resume grants removal ownership only for the recorded live kernel queue.
// The caller must retain its exclusive journal lock and load trusted ownership
// records from that journal. Keys and records are never producer authority.
func Resume(identity Identity, epoch ipcepoch.Epoch) (*Queue, error) {
	current, err := ipcepoch.Current()
	if err != nil {
		return nil, err
	}
	if current != epoch || identity.ID < 0 || identity.Key <= 0 || identity.Changed <= 0 {
		return nil, ErrInvalid
	}
	queue, err := Open(identity.Key)
	if err != nil {
		return nil, err
	}
	current, err = ipcepoch.Current()
	if err != nil || current != epoch || queue.Identity() != identity {
		return nil, errors.Join(ErrInvalid, err, queue.Close())
	}
	queue.owned = true
	return queue, nil
}
