//go:build !darwin && !linux

package sqlitemailbox

import (
	"context"
	"os"
)

func Open(context.Context, string) (*Mailbox, error) { return nil, ErrRequest }

func verifyMailboxPaths(string, *os.File, *os.File) error { return ErrClosed }
func closeMailboxFiles(*os.File, *os.File) error          { return nil }
