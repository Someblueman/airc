//go:build !darwin && !linux

package store

import "errors"

type files struct{}

func openFiles(string) (*files, error) {
	return nil, errors.New("journal ownership and local filesystem verification unavailable")
}
func (*files) close() error  { return nil }
func (*files) verify() error { return nil }
