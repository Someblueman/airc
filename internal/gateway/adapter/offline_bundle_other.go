//go:build !darwin && !linux

package adapter

func ReadOfflineBundle(string) (OfflineBundle, error) {
	return OfflineBundle{}, ErrOfflineBundleUnsupported
}
