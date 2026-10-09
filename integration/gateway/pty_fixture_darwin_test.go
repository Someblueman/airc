//go:build darwin

package gateway_test

import (
	"bytes"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

func openPTY() (int, string, error) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, "", err
	}
	if err := unix.IoctlSetInt(master, unix.TIOCPTYGRANT, 0); err != nil {
		_ = unix.Close(master)
		return -1, "", err
	}
	if err := unix.IoctlSetInt(master, unix.TIOCPTYUNLK, 0); err != nil {
		_ = unix.Close(master)
		return -1, "", err
	}
	var name [128]byte
	// x/sys has no variable-buffer ioctl wrapper. Keep this raw call confined
	// to the native PTY test fixture; production UART uses libSystem wrappers.
	//lint:ignore SA1019 TIOCPTYGNAME needs a bounded output buffer; no exported x/sys wrapper exists.
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(master), uintptr(unix.TIOCPTYGNAME), uintptr(unsafe.Pointer(&name[0])))
	if errno != 0 {
		_ = unix.Close(master)
		return -1, "", errno
	}
	end := bytes.IndexByte(name[:], 0)
	if end < 1 {
		_ = unix.Close(master)
		return -1, "", errors.New("PTY name is empty or unterminated")
	}
	return master, string(name[:end]), nil
}

func readPTYTermios(fd int) (unix.Termios, error) {
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return unix.Termios{}, err
	}
	return *t, nil
}

func ptyBaud115200(t unix.Termios) bool {
	return t.Ispeed == unix.B115200 && t.Ospeed == unix.B115200
}

// Start with distinct input/output rates so startup must replace both, and
// shutdown must restore the actual kernel settings.
func preparePTY(fd int) (unix.Termios, error) {
	t, err := readPTYTermios(fd)
	if err != nil {
		return unix.Termios{}, err
	}
	t.Ispeed, t.Ospeed = unix.B9600, unix.B19200
	if err := unix.IoctlSetTermios(fd, unix.TIOCSETA, &t); err != nil {
		return unix.Termios{}, err
	}
	t, err = readPTYTermios(fd)
	if err != nil {
		return unix.Termios{}, err
	}
	if t.Ispeed != unix.B9600 || t.Ospeed != unix.B19200 {
		return unix.Termios{}, fmt.Errorf("PTY did not retain distinct 9600 input / 19200 output baud: %+v", t)
	}
	return t, nil
}
