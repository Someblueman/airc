package main

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type desktopNotifier struct {
	enabled              bool
	quietStart, quietEnd int
	quiet                bool
	last                 time.Time
	now                  func() time.Time
	send                 func(context.Context, string, string) error
}

func newDesktopNotifier(enabled bool, quietHours string) (*desktopNotifier, error) {
	n := &desktopNotifier{enabled: enabled, now: time.Now, send: sendDesktopNotification}
	if quietHours != "" {
		parts := strings.Split(quietHours, "-")
		if len(parts) != 2 {
			return nil, errors.New("quiet hours must be HH:MM-HH:MM in local time")
		}
		start, err := time.Parse("15:04", parts[0])
		if err != nil {
			return nil, err
		}
		end, err := time.Parse("15:04", parts[1])
		if err != nil {
			return nil, err
		}
		n.quiet, n.quietStart, n.quietEnd = true, start.Hour()*60+start.Minute(), end.Hour()*60+end.Minute()
	}
	return n, nil
}

func (n *desktopNotifier) notify(ctx context.Context, title, body string) error {
	if !n.enabled {
		return nil
	}
	now := n.now()
	minute := now.Hour()*60 + now.Minute()
	if n.quiet {
		if n.quietStart == n.quietEnd || n.quietStart < n.quietEnd && minute >= n.quietStart && minute < n.quietEnd || n.quietStart > n.quietEnd && (minute >= n.quietStart || minute < n.quietEnd) {
			return nil
		}
	}
	if !n.last.IsZero() && now.Sub(n.last) < 10*time.Second {
		return nil
	}
	n.last = now
	body = ellipsize(cleanText(body), 161)
	limited, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return n.send(limited, cleanText(title), body)
}

func sendDesktopNotification(ctx context.Context, title, body string) error {
	switch runtime.GOOS {
	case "darwin":
		// Content is argv, never executable AppleScript or shell text.
		script := "on run argv\ndisplay notification (item 2 of argv) with title (item 1 of argv)\nend run"
		return exec.CommandContext(ctx, "osascript", "-e", script, title, body).Run()
	case "linux":
		return exec.CommandContext(ctx, "notify-send", "--", title, body).Run()
	default:
		return errors.New("desktop notifications supported on macOS and Linux")
	}
}
