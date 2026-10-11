package auth

import (
	"io"
	"strings"
	"unicode/utf8"
)

// ReadICECredential reads an owner-protected TURN password without imposing
// a hexadecimal secret format on the external service.
func ReadICECredential(path string) (string, error) {
	f, err := openPrivate(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4097))
	defer clear(data)
	if err != nil || len(data) > 4096 || !utf8.Valid(data) {
		return "", ErrCredential
	}
	value := strings.TrimSuffix(string(data), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n\x00") {
		return "", ErrCredential
	}
	return value, nil
}
