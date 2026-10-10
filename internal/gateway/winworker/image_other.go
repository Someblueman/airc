//go:build !windows

package winworker

import (
	"errors"
	"os"
)

// Pin locks and verifies the provisioned native worker until the caller closes it.
func Pin(string, string) (*os.File, error) {
	return nil, errors.New("native worker requires Windows")
}
