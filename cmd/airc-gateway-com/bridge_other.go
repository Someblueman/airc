//go:build !windows

package main

import (
	"context"
	"errors"
)

func exchange(context.Context, string, options, []byte) ([]byte, error) {
	return nil, errors.New("COM bridge requires Windows")
}
