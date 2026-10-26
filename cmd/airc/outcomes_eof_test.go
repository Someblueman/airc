package main

import (
	"fmt"
	"io"
	"testing"
)

// A server that closes the connection before registration completes, as a
// daemon restarting while a blocking check reconnects does, is an outage the
// check keeps retrying within its deadline, not a refusal that ends it.
func TestClosedBeforeRegistrationIsARetryableOutage(t *testing.T) {
	err := fmt.Errorf("register with airc: %w", fmt.Errorf("server closed connection before registration: %w", io.ErrUnexpectedEOF))
	f := failure(err, "login")
	if f.Code != "server_unavailable" || !f.Retryable {
		t.Fatalf("code %q retryable %v", f.Code, f.Retryable)
	}
}
