// Package bytesize parses and formats byte counts such as 512MiB or 1GB for
// command-line flags.
package bytesize

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var units = []struct {
	suffix string
	scale  float64
}{
	{"tib", 1 << 40}, {"gib", 1 << 30}, {"mib", 1 << 20}, {"kib", 1 << 10},
	{"tb", 1e12}, {"gb", 1e9}, {"mb", 1e6}, {"kb", 1e3},
	{"t", 1 << 40}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10},
	{"b", 1},
}

// Parse reads a non-negative byte count: a number with an optional unit.
// KiB, MiB, GiB and TiB (and the bare K, M, G and T) are powers of 1024;
// KB, MB, GB and TB are powers of 1000. Units ignore case.
func Parse(text string) (int64, error) {
	s := strings.ToLower(strings.TrimSpace(text))
	scale := 1.0
	for _, unit := range units {
		if number, found := strings.CutSuffix(s, unit.suffix); found {
			s, scale = strings.TrimSpace(number), unit.scale
			break
		}
	}
	value, err := strconv.ParseFloat(s, 64)
	if err != nil || value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
		return 0, fmt.Errorf("%q is not a size such as 512MiB or 1GB", text)
	}
	bytes := value * scale
	if bytes >= math.MaxInt64 {
		return 0, errors.New("size is too large")
	}
	return int64(bytes), nil
}

// Format writes a byte count with the largest binary unit that keeps it whole.
func Format(bytes int64) string {
	for _, unit := range []struct {
		suffix string
		scale  int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if bytes >= unit.scale && bytes%unit.scale == 0 {
			return strconv.FormatInt(bytes/unit.scale, 10) + unit.suffix
		}
	}
	return strconv.FormatInt(bytes, 10)
}
