//go:build !windows

package main

import (
	"context"
	"errors"
)

func runChild(context.Context, string, options) error {
	return errors.New("named-pipe helper requires Windows")
}
