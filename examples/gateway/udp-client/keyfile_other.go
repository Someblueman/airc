//go:build !darwin && !linux

package main

import "errors"

func strictKeyFile(string, bool) ([]byte, error) {
	return nil, errors.New("UDP example requires a supported strict-key platform")
}
