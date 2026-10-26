//go:build !darwin && !linux

package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/gateway/internal/config"
)

func RunFileMailbox(context.Context, config.Config) error {
	return errors.New("file mailbox is unavailable on this platform")
}
