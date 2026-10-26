//go:build darwin || linux

package service

import (
	"os"
	"syscall"
)

func unixgramOwner(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uint32(stat.Uid) == uint32(os.Geteuid())
}

func unixgramRootOwned(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == 0
}

func unixgramTrustedAncestor(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	if uint32(stat.Uid) != uint32(os.Geteuid()) && stat.Uid != 0 {
		return false
	}
	return info.Mode().Perm()&022 == 0 || info.Mode()&os.ModeSticky != 0 && stat.Uid == 0
}
