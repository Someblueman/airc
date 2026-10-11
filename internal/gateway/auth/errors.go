// Package auth reads owner-provisioned credential files and verifies provider
// protocols (pinned TLS and signed syslog origins, SNMPv3 USM, IRC and XMPP
// bridge sessions). It is not an identity authority for AIRC: requests name
// their author and AIRC decides who that may be.
package auth

import (
	"errors"
	"io"

	"github.com/Someblueman/airc/internal/gateway/core"
)

var (
	ErrAuthentication = errors.New(string(core.AuthenticationFailed))
	ErrForbidden      = errors.New(string(core.Forbidden))
	ErrCredential     = errors.New("credential_file_invalid")
	ErrPlatform       = errors.New("credential filesystem verification unavailable on this platform")
)

// ClockSkewSeconds is the tolerated clock difference for provider event
// timestamps and request issue times.
const ClockSkewSeconds int64 = 60

// keyFile reads a bounded owner-private (or, for public material, owner-
// writable-only) key file through the platform credential-file checks.
func keyFile(path string, private bool) ([]byte, error) {
	open := openPublic
	if private {
		open = openPrivate
	}
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8193))
	if err != nil || len(data) > 8192 {
		clear(data)
		return nil, ErrCredential
	}
	return data, nil
}

// MaxResponsePayloadBytes bounds one result written back to a provider
// (an IRC or XMPP bridge reply).
const MaxResponsePayloadBytes = 32 << 10

var ErrResponseCapacity = errors.New("response_capacity")
