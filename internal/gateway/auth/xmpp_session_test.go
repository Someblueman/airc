//go:build unix

package auth

import (
	"context"
	"testing"
	"time"
)

func TestXMPPVerifiedSessionAndJoinedCancellation(t *testing.T) {
	s, _ := xmppSessionFixture(t, "valid")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	x, err := s.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if x.conn.ConnectionState().ServerName != "localhost" {
		t.Fatal("SNI differs from account domain")
	}
	cancel()
	x.Close()
	x.Close()
	s.Close()
	if _, err = s.Connect(context.Background()); err == nil {
		t.Fatal("closed credentials usable")
	}
}

func TestXMPPSessionAuthenticationRefusals(t *testing.T) {
	for _, mode := range []string{"tls12", "unknown_ca", "failure", "wrong_account"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := xmppSessionFixture(t, mode)
			if x, err := s.Connect(context.Background()); err == nil {
				x.Close()
				t.Fatal("refusal accepted")
			}
		})
	}
}

func TestXMPPSessionCancellationJoinsBlockedIO(t *testing.T) {
	for _, mode := range []string{"blocked_tls", "blocked_sasl"} {
		t.Run(mode, func(t *testing.T) {
			s, entered := xmppSessionFixture(t, mode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				x, err := s.Connect(ctx)
				if x != nil {
					x.Close()
				}
				done <- err
			}()
			<-entered
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancel accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("cancel did not join")
			}
		})
	}
}

func TestXMPPSessionInitialDeadlineAndCredentialClear(t *testing.T) {
	s, _ := xmppSessionFixture(t, "blocked_tls")
	start := time.Now()
	if x, err := s.Connect(context.Background()); err == nil {
		x.Close()
		t.Fatal("blocked TLS accepted")
	}
	if elapsed := time.Since(start); elapsed < 2800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatal("initial budget:", elapsed)
	}
	password := s.password
	s.Close()
	for _, b := range password {
		if b != 0 {
			t.Fatal("credential retained after close")
		}
	}
}
