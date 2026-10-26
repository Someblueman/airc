//go:build !darwin && !linux

package service

import "os"

func unixgramOwner(os.FileInfo) bool           { return false }
func unixgramRootOwned(os.FileInfo) bool       { return false }
func unixgramTrustedAncestor(os.FileInfo) bool { return false }
