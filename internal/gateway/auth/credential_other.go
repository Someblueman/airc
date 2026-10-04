//go:build !unix

package auth

import "os"

// Native ACL/ownership validation is not implemented on non-Unix platforms.
// Refuse credential operations instead of treating POSIX mode bits as an ACL.
func openPrivate(string) (*os.File, error) { return nil, ErrPlatform }
func createPrivate(string, []byte) error   { return ErrPlatform }
