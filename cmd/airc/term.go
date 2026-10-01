package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
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

const (
	enterScreen = "\x1b[?1049h\x1b[?7l\x1b[2J" // alternate screen, no autowrap, cleared
	leaveScreen = "\x1b[?25h\x1b[?7h\x1b[?1049l"
	hideCursor  = "\x1b[?25l"
	showCursor  = "\x1b[?25h"
)
