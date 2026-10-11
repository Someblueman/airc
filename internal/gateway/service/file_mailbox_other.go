//go:build !darwin && !linux

package service

import (
	"context"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/config"
)

func RunFileMailbox(context.Context, config.Config) error {
	return errors.New("file mailbox is unavailable on this platform")
}
