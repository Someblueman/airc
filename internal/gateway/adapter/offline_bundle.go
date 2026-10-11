package adapter

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const MaxOfflineBundleBytes = 1 << 20
const MaxOfflineBundleEntries = 32

var ErrOfflineBundle = errors.New("invalid offline signed-message bundle")
var ErrOfflineBundleUnsupported = errors.New("offline bundle files are unsupported on this platform")

// OfflineBundle groups either plain contract requests, each naming its
// author like a request on any other carrier, or (the older form) opaque
// compact signed envelopes that each need authentication before admission.
// The container itself conveys no identity or authority: for plain requests
// the local operator who runs the import is the access gate. This format
// contains no executable attachments.
type OfflineBundle struct {
	Version   int               `json:"version"`
	Envelopes []string          `json:"envelopes,omitempty"`
	Requests  []json.RawMessage `json:"requests,omitempty"`
}

func DecodeOfflineBundle(data []byte) (OfflineBundle, error) {
	var bundle OfflineBundle
	// Plain requests are raw objects checked later by the contract decoder;
	// the strict schema walk would read json.RawMessage as an array.
	var members map[string]json.RawMessage
	if jsoninput.Validate(data, MaxOfflineBundleBytes) != nil || json.Unmarshal(data, &members) != nil || members == nil {
		return OfflineBundle{}, ErrOfflineBundle
	}
	for name := range members {
		if name != "version" && name != "envelopes" && name != "requests" {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	if json.Unmarshal(data, &bundle) != nil || bundle.Version != 1 || (len(bundle.Envelopes) == 0) == (len(bundle.Requests) == 0) || len(bundle.Envelopes) > MaxOfflineBundleEntries || len(bundle.Requests) > MaxOfflineBundleEntries {
		return OfflineBundle{}, ErrOfflineBundle
	}
	for _, request := range bundle.Requests {
		if len(request) == 0 || len(request) > MaxRequestFrameBytes || request[0] != '{' {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	for _, token := range bundle.Envelopes {
		if len(token) == 0 || len(token) > auth.MaxSignedBytes {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	return bundle, nil
}
