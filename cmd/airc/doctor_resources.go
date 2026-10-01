package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type processResources struct {
	PID       int     `json:"pid"`
	OpenFiles *int    `json:"open_files,omitempty"`
	SoftLimit *uint64 `json:"soft_limit,omitempty"`
	HardLimit *uint64 `json:"hard_limit,omitempty"`
	Error     string  `json:"error,omitempty"`
}

// Only the current process's limits are available portably. A host selected
// with --pid has a descriptor count, never an inferred limit from its child.
func inspectResources(ctx context.Context, pid int) processResources {
	r := processResources{PID: pid}
	if pid == os.Getpid() {
		var limit syscall.Rlimit
		if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &limit); err == nil {
			r.SoftLimit, r.HardLimit = &limit.Cur, &limit.Max
		}
	}
	if entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid)); err == nil {
		count := len(entries)
		r.OpenFiles = &count
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	data, err := exec.CommandContext(ctx, "lsof", "-nP", "-p", strconv.Itoa(pid), "-F", "f").Output()
	if err != nil {
		r.Error = "descriptor count unavailable: " + err.Error()
		return r
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if len(line) > 1 && line[0] == 'f' && line[1] >= '0' && line[1] <= '9' {
			count++
		}
	}
	r.OpenFiles = &count
	return r
}

type cursorHealth struct {
	Path   string       `json:"path"`
	Locked bool         `json:"locked"`
	Owner  *cursorOwner `json:"owner,omitempty"`
	Error  string       `json:"error,omitempty"`
}

func inspectCursor(opt options, nick string) *cursorHealth {
	path, _, err := cursorPath(opt, nick)
	if err != nil {
		return &cursorHealth{Error: err.Error()}
	}
	h := &cursorHealth{Path: path}
	if data, err := os.ReadFile(path); err == nil {
		var store cursorStore
		if err := json.Unmarshal(data, &store); err != nil {
			h.Error = "corrupt cursor file: " + err.Error()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		h.Error = err.Error()
	}
	file, err := os.Open(path + ".lock")
	if errors.Is(err, os.ErrNotExist) {
		return h
	}
	if err != nil {
		h.Error = err.Error()
		return h
	}
	defer file.Close()
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	} else if errors.Is(err, syscall.EWOULDBLOCK) {
		h.Locked = true
		data, _ := os.ReadFile(path + ".lock")
		var owner cursorOwner
		if json.Unmarshal(data, &owner) == nil && owner.PID > 0 {
			h.Owner = &owner
		}
	} else {
		h.Error = err.Error()
	}
	return h
}
