//go:build linux

package adapter

import "golang.org/x/sys/unix"

func uartGetTermios(fd int) (unix.Termios, error) {
	t, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return unix.Termios{}, err
	}
	return *t, nil
}
func uartSetTermios(fd int, t unix.Termios) error { return unix.IoctlSetTermios(fd, unix.TCSETS, &t) }
func uartClaimTTY(fd int) error                   { return unix.IoctlSetInt(fd, unix.TIOCEXCL, 0) }
func uartReleaseTTY(fd int) error                 { return unix.IoctlSetInt(fd, unix.TIOCNXCL, 0) }

func uartRaw115200(t unix.Termios) unix.Termios {
	t.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON | unix.IXOFF | unix.IXANY | unix.INPCK
	t.Oflag &^= unix.OPOST
	t.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	t.Cflag &^= unix.CBAUD | unix.CIBAUD | unix.CSIZE | unix.CSTOPB | unix.PARENB | unix.PARODD | unix.CRTSCTS
	t.Cflag |= unix.B115200 | unix.CS8 | unix.CLOCAL | unix.CREAD
	t.Ispeed = unix.B115200
	t.Ospeed = unix.B115200
	t.Cc[unix.VMIN] = 1
	t.Cc[unix.VTIME] = 0
	return t
}
