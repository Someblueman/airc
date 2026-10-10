//go:build darwin || linux

package adapter

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// PipeStream owns pollable duplicates of inherited pipe descriptors. It never
// closes the source descriptors. Nonblocking status is shared with the sources
// through the open file descriptions; callers must dedicate them to this session.
type PipeStream struct {
	input, output *os.File
	closeOnce     sync.Once
	closeErr      error
}

// OpenPipeStream rejects files and terminals before any gateway journal opens.
// Both descriptors must be dedicated pipes with the appropriate access mode.
func OpenPipeStream(input, output *os.File) (*PipeStream, error) {
	in, err := duplicatePipe(input, false)
	if err != nil {
		return nil, err
	}
	out, err := duplicatePipe(output, true)
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	return &PipeStream{input: in, output: out}, nil
}

func duplicatePipe(source *os.File, writing bool) (*os.File, error) {
	if source == nil {
		return nil, errors.New("missing pipe descriptor")
	}
	raw, err := source.SyscallConn()
	if err != nil {
		return nil, fmt.Errorf("inspect pipe descriptor: %w", err)
	}
	dup := -1
	var prepareErr error
	err = raw.Control(func(fd uintptr) {
		var stat unix.Stat_t
		if prepareErr = unix.Fstat(int(fd), &stat); prepareErr != nil {
			return
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFIFO {
			prepareErr = errors.New("stdio requires pipe descriptors")
			return
		}
		flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
		if err != nil {
			prepareErr = err
			return
		}
		mode := flags & unix.O_ACCMODE
		if (writing && mode != unix.O_WRONLY) || (!writing && mode != unix.O_RDONLY) {
			prepareErr = errors.New("incorrect pipe access mode")
			return
		}
		dup, prepareErr = unix.FcntlInt(fd, unix.F_DUPFD_CLOEXEC, 3)
	})
	if err != nil || prepareErr != nil {
		if dup >= 0 {
			_ = unix.Close(dup)
		}
		return nil, fmt.Errorf("prepare pipe descriptor: %w", errors.Join(err, prepareErr))
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return nil, fmt.Errorf("prepare pollable pipe: %w", err)
	}
	f := os.NewFile(uintptr(dup), "gateway-pipe")
	if f == nil {
		_ = unix.Close(dup)
		return nil, errors.New("invalid duplicated pipe")
	}
	if err := f.SetDeadline(time.Time{}); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("pipe deadlines unavailable: %w", err)
	}
	return f, nil
}

func (p *PipeStream) Read(b []byte) (int, error)         { return p.input.Read(b) }
func (p *PipeStream) Write(b []byte) (int, error)        { return p.output.Write(b) }
func (p *PipeStream) SetReadDeadline(t time.Time) error  { return p.input.SetReadDeadline(t) }
func (p *PipeStream) SetWriteDeadline(t time.Time) error { return p.output.SetWriteDeadline(t) }
func (p *PipeStream) Close() error {
	p.closeOnce.Do(func() { p.closeErr = errors.Join(p.input.Close(), p.output.Close()) })
	return p.closeErr
}
