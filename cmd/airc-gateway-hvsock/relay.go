package main

import (
	"context"
	"errors"
	"io"
	"net"
	"time"
)

// relay forwards only ciphertext to the operator's fixed numeric destination.
// Each copy owns one direction. Both are joined, including on limit/close errors.
func relay(ctx context.Context, guest net.Conn, address string) error {
	dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	upstream, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", address)
	cancel()
	if err != nil {
		return err
	}
	defer upstream.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("relay requires deadline")
	}
	if err := errors.Join(guest.SetDeadline(deadline), upstream.SetDeadline(deadline)); err != nil {
		return err
	}
	closeBoth := func() {
		_ = guest.Close()
		_ = upstream.Close()
	}
	interrupted := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { closeBoth(); close(interrupted) })
	defer func() {
		if !stop() {
			<-interrupted
		}
	}()
	results := make(chan error, 2)
	copyDirection := func(dst, src net.Conn) {
		limited := &io.LimitedReader{R: src, N: 1 << 20}
		_, err := io.Copy(dst, limited)
		if err == nil && limited.N == 0 {
			err = errors.New("relay ciphertext limit reached")
		}
		if err == nil {
			if half, ok := dst.(interface{ CloseWrite() error }); ok {
				err = half.CloseWrite()
			} else {
				err = errors.New("relay stream cannot close its write direction")
			}
		}
		if err != nil {
			closeBoth()
		}
		results <- err
	}
	go copyDirection(upstream, guest)
	go copyDirection(guest, upstream)
	err = errors.Join(<-results, <-results)
	if err != nil {
		return errors.New("Hyper-V relay ended unsuccessfully; native outcome may be uncertain")
	}
	return ctx.Err()
}
