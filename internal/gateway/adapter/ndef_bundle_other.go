//go:build !darwin && !linux

package adapter

func ReadNDEFBundle(string) (OfflineBundle, error) {
	return OfflineBundle{}, ErrOfflineBundleUnsupported
}
