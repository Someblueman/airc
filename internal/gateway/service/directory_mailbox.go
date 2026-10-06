package service

import "context"

type directoryMailboxOwner interface {
	serve(context.Context) error
	Close() error
}
