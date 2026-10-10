//go:build !((darwin && arm64) || (linux && arm64))

package sharedmem

func checkNative(int, int) error         { return ErrUnsupported }
func mapNative(int, int) ([]byte, error) { return nil, ErrUnsupported }
func unmapNative([]byte) error           { return ErrUnsupported }
func closeNative(int) error              { return ErrUnsupported }
