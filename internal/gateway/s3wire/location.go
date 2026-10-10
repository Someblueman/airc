package s3wire

import (
	"net"
	"regexp"
	"strings"
)

var regionPattern = regexp.MustCompile(`^[a-z]{2}(?:-[a-z0-9]+)+-[0-9]+$`)

// ValidateLocation validates the fixed commercial-region virtual-host bucket
// location and relative object prefix used by this wire client.
func ValidateLocation(region, bucket, account, prefix string) error {
	if !validRegion(region) || !validBucket(bucket) || len(account) != 12 || !digits(account) || !validPrefix(prefix) {
		return ErrRequest
	}
	return nil
}

func validRegion(region string) bool {
	return regionPattern.MatchString(region) &&
		!strings.HasPrefix(region, "cn-") && !strings.HasPrefix(region, "us-gov-") &&
		!strings.HasPrefix(region, "us-iso-") && !strings.HasPrefix(region, "us-isob-") &&
		!strings.HasPrefix(region, "eu-isoe-") && !strings.HasPrefix(region, "us-isof-")
}

func validBucket(bucket string) bool {
	if len(bucket) < 3 || len(bucket) > 63 || !lowerAlnum(bucket[0]) || net.ParseIP(bucket) != nil {
		return false
	}
	last := bucket[len(bucket)-1]
	if !lowerAlnum(last) || strings.HasPrefix(bucket, "xn--") || strings.HasPrefix(bucket, "sthree-") || strings.HasPrefix(bucket, "amzn_s3_demo_") ||
		strings.HasSuffix(bucket, "-s3alias") || strings.HasSuffix(bucket, "--ol-s3") || strings.HasSuffix(bucket, "--x-s3") || strings.HasSuffix(bucket, "--table-s3") || strings.HasSuffix(bucket, ".mrap") {
		return false
	}
	for i := 0; i < len(bucket); i++ {
		b := bucket[i]
		if !lowerAlnum(b) && b != '-' {
			return false
		}
	}
	return true
}

func lowerAlnum(b byte) bool { return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' }

func validPrefix(prefix string) bool {
	if len(prefix) < 1 || len(prefix) > 256 || strings.HasPrefix(prefix, "/") || strings.HasSuffix(prefix, "/") {
		return false
	}
	for _, segment := range strings.Split(prefix, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for i := 0; i < len(segment); i++ {
			b := segment[i]
			if !(b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '.' || b == '_' || b == '-') {
				return false
			}
		}
	}
	return true
}

func digits(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
