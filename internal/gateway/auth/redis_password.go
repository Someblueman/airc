package auth

import "io"

const MaxRedisPassword = 4096

// ReadRedisPassword reads exact password bytes from an existing private regular
// file. It never trims whitespace or interprets bearer-token/hex syntax. The
// caller owns the returned buffer and must clear it after the connection owner
// stops using it. File errors never expose the path or secret content.
func ReadRedisPassword(path string) ([]byte, error) {
	f, err := openPrivate(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxRedisPassword+1))
	if err != nil || len(data) == 0 || len(data) > MaxRedisPassword {
		clear(data)
		return nil, ErrCredential
	}
	return data, nil
}
