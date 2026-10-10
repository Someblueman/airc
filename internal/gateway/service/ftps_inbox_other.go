//go:build !linux

package service

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/config"
	"github.com/Someblueman/airc/internal/gateway/dispatch"
)

func openFTPSInbox(config.Config, config.Listener, *dispatch.Dispatcher, chan struct{}) (mailboxOwner, error) {
	return nil, errors.New("vsftpd FTPS inbox requires Linux")
}
