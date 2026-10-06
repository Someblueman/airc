package config

import (
	"errors"
	"github.com/Someblueman/airc/internal/gateway/core"
	"path/filepath"
)

func validateFileMailbox(l Listener) error {
	if l.Protocol != "file_mailbox" && l.Protocol != "directory_mailbox" {
		return nil
	}
	if !absolute(l.Address) || filepath.Clean(l.Address) != l.Address || l.TLSCertFile != "" || l.TLSKeyFile != "" || len(l.AllowedOrigins) != 0 {
		return errors.New("file mailbox requires a normalized absolute directory without TLS or origins")
	}
	if l.Enabled && !UnixAvailable() {
		return errors.New("file mailbox is unavailable on this platform")
	}
	if len(l.Operations) == 0 {
		return errors.New("file mailbox requires explicit operations")
	}
	for _, op := range l.Operations {
		if op != core.Send && op != core.ReceiptGet && op != core.HistoryRead {
			return errors.New("file mailbox supports send, receipt_get and history_read")
		}
	}
	return nil
}
