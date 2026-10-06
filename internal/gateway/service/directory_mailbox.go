package service

import "context"

type mailboxOwner interface {
	serve(context.Context) error
	Close() error
}
