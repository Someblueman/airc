//go:build linux

package gateway_test

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func openPTY() (int, string, error) {
	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return -1, "", err
	}
	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		_ = unix.Close(master)
		return -1, "", err
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		_ = unix.Close(master)
		return -1, "", err
	}
	return master, fmt.Sprintf("/dev/pts/%d", number), nil
}

func readPTYTermios(fd int) (unix.Termios, error) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return unix.Termios{}, err
	}
	return *t, nil
}

func ptyBaud115200(t unix.Termios) bool {
	input := (t.Cflag & unix.CIBAUD) >> 16
	return t.Cflag&unix.CBAUD == unix.B115200 && (input == 0 || input == unix.B115200)
}

// Start with distinct input/output rates so startup must replace both, and
// shutdown must restore the actual kernel settings.
func preparePTY(fd int) (unix.Termios, error) {
	t, err := readPTYTermios(fd)
	if err != nil {
		return unix.Termios{}, err
	}
	t.Cflag = t.Cflag&^(unix.CBAUD|unix.CIBAUD) | unix.B19200 | (unix.B9600 << 16)
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &t); err != nil {
		return unix.Termios{}, err
	}
	t, err = readPTYTermios(fd)
	if err != nil {
		return unix.Termios{}, err
	}
	if (t.Cflag&unix.CIBAUD)>>16 != unix.B9600 || t.Cflag&unix.CBAUD != unix.B19200 {
		return unix.Termios{}, fmt.Errorf("PTY did not retain distinct 9600 input / 19200 output baud: %+v", t)
	}
	return t, nil
}
