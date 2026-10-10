package auth

import "io"

// ReadCouchDBPassword loads an exact explicit Basic-auth password. It does not
// trim a trailing newline. The caller clears the buffer after the client copies it.
func ReadCouchDBPassword(path string) ([]byte, error) {
	f, err := openPrivate(path)
	if err != nil {
		return nil, ErrCredential
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 257))
	if err != nil || len(data) < 32 || len(data) > 256 || !validCredentialBytes(data) {
		clear(data)
		return nil, ErrCredential
	}
	return data, nil
}
