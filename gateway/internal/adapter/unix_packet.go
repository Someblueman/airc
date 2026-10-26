package adapter

import "errors"

var ErrUnixPacketUnsupported = errors.New("unix sequenced-packet listeners are unsupported on this platform")
