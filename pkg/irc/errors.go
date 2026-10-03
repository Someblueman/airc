package irc

import "fmt"

// RejectedError carries the server numeric, so callers need not parse prose.
type RejectedError struct {
	Code    string
	Message string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("server rejected the request: %s", e.Message)
}
