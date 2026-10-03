// Package tokenfmt defines the shape of airc bearer tokens (32 random bytes,
// hex encoded) so clients and servers validate them identically.
package tokenfmt

import "encoding/hex"

// Valid reports whether token is 64 hex characters.
func Valid(token string) bool {
	if len(token) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(token)
	return err == nil && len(decoded) == 32
}
