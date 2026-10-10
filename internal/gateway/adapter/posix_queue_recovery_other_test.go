//go:build !linux || (!arm64 && !amd64)

package adapter

import (
	"errors"
	"testing"
)

func TestPOSIXQueueRecoveryUnsupported(t *testing.T) {
	if _, err := CurrentPOSIXQueueEpoch(); !errors.Is(err, ErrPOSIXQueueUnsupported) {
		t.Fatal(err)
	}
	if q, err := ResumePOSIXQueue("/queue", 1, POSIXQueueIdentity{Inode: 1}, POSIXQueueEpoch{}); q != nil || !errors.Is(err, ErrPOSIXQueueUnsupported) {
		t.Fatalf("%v %v", q, err)
	}
}
