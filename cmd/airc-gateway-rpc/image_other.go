//go:build !windows

package main

import (
	"errors"
	"os"
)

func pinWorker(string, string) (*os.File, error) {
	return nil, errors.New("RPC worker requires Windows")
}
