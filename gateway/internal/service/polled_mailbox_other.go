//go:build !darwin && !linux

package service

import (
	"errors"

	"github.com/Someblueman/airc/gateway/internal/config"
	"github.com/Someblueman/airc/gateway/internal/dispatch"
)

func openDirectoryMailbox(config.Config, config.Listener, *dispatch.Dispatcher, chan struct{}) (mailboxOwner, error) {
	return nil, errors.New("directory mailbox is unavailable on this platform")
}

func openAppendLog(config.Config, config.Listener, *dispatch.Dispatcher, chan struct{}) (mailboxOwner, error) {
	return nil, errors.New("append log is unavailable on this platform")
}
