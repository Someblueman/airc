//go:build !((darwin && arm64) || (linux && arm64))

package adapter

import (
	"os"

	"github.com/Someblueman/airc/internal/gateway/sharedmem"
)

type mappedFileMapping struct{}

func openMappedFileMapping(*os.Root) (*mappedFileMapping, error) {
	return nil, sharedmem.ErrUnsupported
}
func (*mappedFileMapping) Bytes() []byte   { return nil }
func (*mappedFileMapping) Validate() error { return sharedmem.ErrUnsupported }
func (*mappedFileMapping) Close() error    { return nil }
