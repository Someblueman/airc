// Package auth reads owner-provisioned credential files (tokens, TLS
// certificates, trust roots, cursor keys). It is not an identity authority
// for AIRC: requests name their author and AIRC decides who that may be.
package auth

import (
	"errors"

	"github.com/Someblueman/airc/gateway/internal/core"
)

var (
	ErrAuthentication = errors.New(string(core.AuthenticationFailed))
	ErrForbidden      = errors.New(string(core.Forbidden))
	ErrCredential     = errors.New("credential_file_invalid")
	ErrPlatform       = errors.New("credential filesystem verification unavailable on this platform")
)

// ClockSkewSeconds is the tolerated clock difference for request issue
// times.
const ClockSkewSeconds int64 = 60
