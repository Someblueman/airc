// Package retention parses the history archive's retention flags, shared by
// aircd and airc service install.
package retention

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/bytesize"
)

// Defaults keep a month of history, within 10GiB of disk.
const (
	DefaultAge  = "30d"
	DefaultSize = "10GiB"
)

// ParseAge reads how long archived messages are kept: a Go duration such as
// 72h, a number of days such as 30d, or 0 for no age limit. A limit shorter
// than an hour is refused, since retention is applied hourly.
func ParseAge(text string) (time.Duration, error) {
	text = strings.TrimSpace(text)
	if text == "0" {
		return 0, nil
	}
	var age time.Duration
	if days, found := strings.CutSuffix(text, "d"); found {
		n, err := strconv.Atoi(days)
		if err != nil || n < 0 || n > 36500 {
			return 0, fmt.Errorf("%q is not an age such as 30d or 72h", text)
		}
		age = time.Duration(n) * 24 * time.Hour
	} else {
		var err error
		if age, err = time.ParseDuration(text); err != nil {
			return 0, fmt.Errorf("%q is not an age such as 30d or 72h", text)
		}
	}
	if age != 0 && age < time.Hour {
		return 0, errors.New("a history age limit must be at least 1h, or 0 for none")
	}
	return age, nil
}

// ParseSize reads the archive's disk limit: a size such as 10GiB, or 0 for
// no size limit. Anything smaller than 64MiB, one segment, is refused.
func ParseSize(text string) (int64, error) {
	size, err := bytesize.Parse(text)
	if err != nil {
		return 0, err
	}
	if size != 0 && size < 64<<20 {
		return 0, errors.New("a history size limit must be at least 64MiB, or 0 for none")
	}
	return size, nil
}
