//go:build unix

package auth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func TestIRCTLSRegistrationSnapshotAndCancellationJoin(t *testing.T) {
	server, b, _ := ircSessionFixture(t, "valid")
	if err := os.WriteFile(b.PasswordFile, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	b.Account = "other"
	b.Routes[0].Room = "#changed"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	session, err := server.Connect(ctx)
	if err != nil || session == nil || !session.registration.Ready() {
		t.Fatal("verified registration", err)
	}
	defer session.Close()
	cancel()
	session.Close()
	session.Close()
	if session.registration.Ready() {
		t.Fatal("closed readiness retained")
	}
	original := server.password
	server.Close()
	if len(server.password) != 0 || !bytes.Equal(original, make([]byte, len(original))) {
		t.Fatal("credential memory retained")
	}
}

func TestIRCTLSAndAccountRefusals(t *testing.T) {
	for _, mode := range []string{"wrong_name", "unknown_ca", "TLS12", "wrong_account"} {
		t.Run(mode, func(t *testing.T) {
			server, _, _ := ircSessionFixture(t, mode)
			session, err := server.Connect(t.Context())
			if !errors.Is(err, ErrAuthentication) || session != nil {
				if session != nil {
					session.Close()
				}
				t.Fatal("unverified registration accepted", err)
			}
		})
	}
}

func TestIRCBlockedHandshakeAndRegistrationCancelJoin(t *testing.T) {
	for _, mode := range []string{"blocked_tls", "blocked_registration"} {
		t.Run(mode, func(t *testing.T) {
			server, _, entered := ircSessionFixture(t, mode)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() {
				session, err := server.Connect(ctx)
				if session != nil {
					session.Close()
				}
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("server not entered")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, ErrAuthentication) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("connect not joined")
			}
		})
	}
}

func TestIRCCompleteRegistrationDeadlineCannotBeRenewed(t *testing.T) {
	server, _, _ := ircSessionFixture(t, "trickle_registration")
	started := time.Now()
	session, err := server.Connect(t.Context())
	if session != nil {
		session.Close()
	}
	elapsed := time.Since(started)
	if err != ErrAuthentication || session != nil || elapsed < 2500*time.Millisecond || elapsed > 4500*time.Millisecond {
		t.Fatal("registration deadline", elapsed, err)
	}
}
