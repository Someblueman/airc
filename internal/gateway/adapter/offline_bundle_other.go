//go:build !darwin && !linux

package adapter

import "github.com/Someblueman/airc/internal/gateway/bpv7"

func ReadOfflineBundle(string) (OfflineBundle, error) {
	return OfflineBundle{}, ErrOfflineBundleUnsupported
}

func ReadBPv7(string) (bpv7.Bundle, error) { return bpv7.Bundle{}, ErrOfflineBundleUnsupported }
