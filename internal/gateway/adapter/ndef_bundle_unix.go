//go:build darwin || linux

package adapter

// ReadNDEFBundle only reads a bounded regular file. The NDEF wrapper carries
// no identity or authority; its signed envelopes are authenticated separately.
func ReadNDEFBundle(path string) (OfflineBundle, error) {
	data, err := readBundleFile(path, MaxNDEFBytes, ErrNDEFBundle)
	if err != nil {
		return OfflineBundle{}, err
	}
	defer clear(data)
	return DecodeNDEFBundle(data)
}
