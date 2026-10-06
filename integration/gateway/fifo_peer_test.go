//go:build darwin || linux

package gateway_test

import (
	"errors"
	"io"
	"os"
	"time"

	"github.com/Someblueman/airc/internal/gateway/adapter"
)

// The producer must bound initial return contact too: FIFO EOF before the child
// opens its writer is not established-child shutdown. After a byte, normal EOF
// applies. This peer has one reader; its deadlines are set by that reader.
type fifoPeer struct {
	*adapter.PipeStream
	started      bool
	readDeadline time.Time
}

func (p *fifoPeer) SetReadDeadline(t time.Time) error {
	p.readDeadline = t
	return p.PipeStream.SetReadDeadline(t)
}
func (p *fifoPeer) Read(b []byte) (int, error) {
	for {
		n, err := p.PipeStream.Read(b)
		if n > 0 {
			p.started = true
		}
		if p.started || !errors.Is(err, io.EOF) || len(b) == 0 {
			return n, err
		}
		if p.readDeadline.IsZero() || !time.Now().Before(p.readDeadline) {
			return 0, os.ErrDeadlineExceeded
		}
		timer := time.NewTimer(10 * time.Millisecond)
		<-timer.C
	}
}
