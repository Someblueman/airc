package protocol

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ValidWakePrefix reports whether prefix may be an entry of aircd's
// --wake-allow list: an http or https URL with a host and a plain path
// ending in "/", so a prefix ends inside a host it names (https://ntfy.sh/
// cannot match https://ntfy.sh.example/) and at a path segment
// (https://hooks.example/airc/ cannot match /aircx).
func ValidWakePrefix(prefix string) bool {
	u, err := url.Parse(prefix)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && strings.HasSuffix(u.Path, "/") && PlainWakePath(u.Path) && u.String() == prefix && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

// PlainWakePath reports a path that every web server resolves to itself:
// it starts with "/", holds only letters, digits and "-._~/", and has no
// empty segment ("//") or segment starting with "." save a trailing "/",
// so no receiver can read a dot segment, a ";" parameter or an escape as a
// way out of the prefix it starts with.
func PlainWakePath(path string) bool {
	if !strings.HasPrefix(path, "/") {
		return false
	}
	segments := strings.Split(path[1:], "/")
	for i, segment := range segments {
		if segment == "" && i != len(segments)-1 || strings.HasPrefix(segment, ".") {
			return false
		}
		for _, r := range segment {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-._~", r)) {
				return false
			}
		}
	}
	return true
}

// QuietHours is a daily window, in the daemon's local time, during which
// wake notices are held: "22:00-08:00" runs over midnight. Start and End are
// minutes after midnight; the window is [Start, End).
type QuietHours struct {
	Start, End int
}

// ParseQuietHours reads HH:MM-HH:MM. Start and end must differ: a window
// that covers the whole day would never let a notice out.
func ParseQuietHours(text string) (QuietHours, error) {
	from, to, ok := strings.Cut(text, "-")
	if !ok {
		return QuietHours{}, errors.New("quiet hours must be HH:MM-HH:MM in local time")
	}
	minutes := func(clock string) (int, error) {
		t, err := time.Parse("15:04", clock)
		if err != nil {
			return 0, errors.New("quiet hours must be HH:MM-HH:MM in local time")
		}
		return t.Hour()*60 + t.Minute(), nil
	}
	start, err := minutes(from)
	if err != nil {
		return QuietHours{}, err
	}
	end, err := minutes(to)
	if err != nil {
		return QuietHours{}, err
	}
	if start == end {
		return QuietHours{}, errors.New("quiet hours need a start and an end that differ")
	}
	return QuietHours{Start: start, End: end}, nil
}

// String writes the window back as HH:MM-HH:MM.
func (q QuietHours) String() string {
	return fmt.Sprintf("%02d:%02d-%02d:%02d", q.Start/60, q.Start%60, q.End/60, q.End%60)
}

// Contains reports whether now, in its own location, falls in the window.
func (q QuietHours) Contains(now time.Time) bool {
	minute := now.Hour()*60 + now.Minute()
	if q.Start < q.End {
		return minute >= q.Start && minute < q.End
	}
	return minute >= q.Start || minute < q.End
}

// Until is when the window that holds now ends: the next End, on the day
// of now or the day after. It is now itself outside the window.
func (q QuietHours) Until(now time.Time) time.Time {
	if !q.Contains(now) {
		return now
	}
	end := time.Date(now.Year(), now.Month(), now.Day(), q.End/60, q.End%60, 0, 0, now.Location())
	if !end.After(now) {
		end = end.AddDate(0, 0, 1)
	}
	return end
}
