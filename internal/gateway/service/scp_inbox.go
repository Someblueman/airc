package service

import (
	"context"
	"errors"
	"os"

	"github.com/Someblueman/airc/internal/gateway/adapter"
	"github.com/Someblueman/airc/internal/gateway/config"
)

// RunSCPInbox consumes one OpenSSH forced-command upload. The original command
// is a syntax restriction, not proof of the SSH identity or native authority.
func RunSCPInbox(ctx context.Context, c config.Config, stream adapter.FramedStream, originalCommand string) error {
	if stream == nil {
		return errors.New("missing SCP input stream")
	}
	if originalCommand != "scp -t inbox" {
		_ = stream.Close()
		return errors.New("SCP inbox requires exactly scp -t inbox")
	}
	return runLocalStream(ctx, c, stream, "scp_inbox")
}

func scpInboxBinding(c config.Config, listener config.Listener) (string, error) {
	route := listener.SCPInbox
	if route == nil || os.Geteuid() <= 0 || uint64(os.Geteuid()) != uint64(route.LoginUID) {
		return "", errors.New("SCP inbox login UID does not match its configured route")
	}
	for _, principal := range c.Principals {
		if principal.ID == route.Principal && !principal.Revoked && principal.Binding != "" {
			return principal.Binding, nil
		}
	}
	return "", errors.New("SCP inbox has no active native binding")
}
