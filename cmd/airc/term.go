package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

// enterRawMode puts the terminal behind f into raw mode, so keys arrive one at a
// time without echo, and returns a function that restores it. It uses stty so the
// same code works on every Unix without platform-specific ioctl constants.
func enterRawMode(f *os.File) (restore func(), err error) {
	get := exec.Command("stty", "-g")
	get.Stdin = f
	saved, err := get.Output()
	if err != nil {
		return nil, errors.New("not a terminal")
	}
	state := strings.TrimSpace(string(saved))
	set := exec.Command("stty", "raw", "-echo")
	set.Stdin = f
	if err := set.Run(); err != nil {
		return nil, err
	}
	return func() {
		reset := exec.Command("stty", state)
		reset.Stdin = f
		_ = reset.Run()
	}, nil
}

// enterScreen opens the alternate screen without autowrap, cleared, with
// bracketed paste and xterm's modifyOtherKeys at level 1, so Shift-Enter
// arrives as CSI 27;2;13~ rather than as a plain Enter; terminals without
// the mode ignore the request. leaveScreen undoes each in turn.
const (
	enterScreen = "\x1b[?1049h\x1b[?7l\x1b[2J\x1b[?2004h\x1b[>4;1m"
	leaveScreen = "\x1b[>4m\x1b[?2004l\x1b[?25h\x1b[?7h\x1b[?1049l"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
)

// terminalSize returns the columns and rows of the terminal attached to f, or 0, 0.
func terminalSize(f *os.File) (cols, rows int) {
	var size struct{ rows, cols, x, y uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	if errno != 0 {
		return 0, 0
	}
	return int(size.cols), int(size.rows)
}

// terminalWidth returns the width of the terminal attached to f, or 0.
func terminalWidth(f *os.File) int {
	cols, _ := terminalSize(f)
	return cols
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// colorFlag defines --color on fs, as watch and ui share it; checkColorMode
// validates what was given.
func colorFlag(fs *flag.FlagSet) *string {
	return fs.String("color", "auto", "colored output: auto, always, or never (auto also honors NO_COLOR)")
}

func checkColorMode(mode string) error {
	if mode != "auto" && mode != "always" && mode != "never" {
		return errors.New("--color must be auto, always, or never")
	}
	return nil
}

// useColor decides whether to emit ANSI styling: only for a real terminal
// unless forced, and never when NO_COLOR is set or the terminal is "dumb".
func useColor(mode string, out io.Writer) bool {
	switch mode {
	case "always":
		return true
	case "never":
		return false
	}
	file, ok := out.(*os.File)
	return ok && isTerminal(file) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb"
}

// outputWidth picks the wrap width: --width, else the terminal, else $COLUMNS,
// capped so very wide terminals still produce readable lines.
func outputWidth(requested int, out io.Writer) int {
	width := requested
	if width <= 0 {
		if file, ok := out.(*os.File); ok {
			width = terminalWidth(file)
		}
	}
	if width <= 0 {
		width, _ = strconv.Atoi(os.Getenv("COLUMNS"))
	}
	if width <= 0 {
		width = 100
	}
	if requested <= 0 {
		width = min(width-1, 120)
	}
	return width
}
