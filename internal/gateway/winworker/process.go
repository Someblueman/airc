package winworker

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"time"
)

// Worker owns one pinned native process and its private standard pipes.
// Its caller must Close it on every path; Finish withholds success until EOF
// and process exit. Native workers establish their own job and watchdog limits.
type Worker struct {
	Input  io.WriteCloser
	Output io.ReadCloser
	cmd    *exec.Cmd
	image  *os.File
	waited bool
	closed bool
}

func Start(ctx context.Context, path, digest string, args ...string) (_ *Worker, result error) {
	image, err := Pin(path, digest)
	if err != nil {
		return nil, err
	}
	w := &Worker{image: image, cmd: exec.CommandContext(ctx, path, args...)}
	defer func() {
		if result != nil {
			_ = w.Close()
		}
	}()
	w.cmd.WaitDelay = 2 * time.Second
	w.cmd.Stderr = io.Discard
	w.Input, err = w.cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	w.Output, err = w.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := w.cmd.Start(); err != nil {
		return nil, errors.New("could not start native worker")
	}
	return w, nil
}

func (w *Worker) Finish() error {
	if w.closed || w.waited {
		return errors.New("native worker is already finished")
	}
	if err := w.Input.Close(); err != nil {
		return err
	}
	var extra [1]byte
	if n, err := io.ReadFull(w.Output, extra[:]); n != 0 || !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing native worker output")
	}
	err := w.cmd.Wait()
	w.waited = true
	if err != nil {
		return errors.New("native worker failed or expired; native outcome may be uncertain")
	}
	return nil
}

func (w *Worker) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if w.cmd.Process != nil && !w.waited {
		_ = w.cmd.Process.Kill()
		_ = w.cmd.Wait()
		w.waited = true
	}
	var result error
	for _, closer := range []io.Closer{w.Input, w.Output, w.image} {
		if closer != nil {
			if err := closer.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}
