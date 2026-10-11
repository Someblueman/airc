package adapter

import (
	"encoding/json"
	"errors"

	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const MaxOfflineBundleBytes = 1 << 20
const MaxOfflineBundleEntries = 32

var ErrOfflineBundle = errors.New("invalid offline bundle")
var ErrOfflineBundleUnsupported = errors.New("offline bundle files are unsupported on this platform")

// OfflineBundle groups plain contract requests, each naming its author like
// a request on any other carrier. The container itself conveys no identity
// or authority: the local operator who runs the import is the access gate.
// This format contains no executable attachments.
type OfflineBundle struct {
	Version  int               `json:"version"`
	Requests []json.RawMessage `json:"requests"`
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
		if name != "version" && name != "requests" {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	if json.Unmarshal(data, &bundle) != nil || bundle.Version != 1 || len(bundle.Requests) == 0 || len(bundle.Requests) > MaxOfflineBundleEntries {
		return OfflineBundle{}, ErrOfflineBundle
	}
	for _, request := range bundle.Requests {
		if len(request) == 0 || len(request) > MaxRequestFrameBytes || request[0] != '{' {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	return bundle, nil
}
