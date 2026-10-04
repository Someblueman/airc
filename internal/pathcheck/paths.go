// Package pathcheck protects credentials and state from aliased output paths.
package pathcheck

import (
	"fmt"
	"os"
	"path/filepath"
)

func Same(left, right string) bool {
	canonical := func(path string) string {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			return resolved
		}
		if parent, err := filepath.EvalSymlinks(filepath.Dir(absolute)); err == nil {
			return filepath.Join(parent, filepath.Base(absolute))
		}
		return absolute
	}
	if canonical(left) == canonical(right) {
		return true
	}
	l, le := os.Stat(left)
	r, re := os.Stat(right)
	return le == nil && re == nil && os.SameFile(l, r)
}

func Distinct(paths ...string) error {
	for i, left := range paths {
		if left == "" {
			continue
		}
		for _, right := range paths[i+1:] {
			if right != "" && Same(left, right) {
				return fmt.Errorf("credential and data files must have distinct paths: %s and %s", left, right)
			}
		}
	}
	return nil
}

// OwnerOnly reports whether info describes a regular file that neither group
// nor others can access, as credentials and private keys must be.
func OwnerOnly(info os.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0
}
