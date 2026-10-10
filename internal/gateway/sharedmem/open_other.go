//go:build !((darwin && arm64) || (linux && arm64))

package sharedmem

func openNative(string) (int, error) { return -1, ErrUnsupported }
