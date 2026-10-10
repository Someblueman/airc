package adapter

import (
	"errors"

	"github.com/Someblueman/airc/internal/gateway/auth"
	"github.com/Someblueman/airc/internal/gateway/jsoninput"
)

const MaxOfflineBundleBytes = 1 << 20
const MaxOfflineBundleEntries = 32

var ErrOfflineBundle = errors.New("invalid offline signed-message bundle")
var ErrOfflineBundleUnsupported = errors.New("offline bundle files are unsupported on this platform")

// OfflineBundle groups opaque compact signed envelopes. Its unsigned container
// conveys no identity, authority or validity; each envelope needs authentication
// before any journal admission. This format contains no executable attachments.
type OfflineBundle struct {
	Version   int      `json:"version"`
	Envelopes []string `json:"envelopes"`
}

func DecodeOfflineBundle(data []byte) (OfflineBundle, error) {
	var bundle OfflineBundle
	if jsoninput.Decode(data, &bundle, MaxOfflineBundleBytes) != nil || bundle.Version != 1 || len(bundle.Envelopes) == 0 || len(bundle.Envelopes) > MaxOfflineBundleEntries {
		return OfflineBundle{}, ErrOfflineBundle
	}
	for _, token := range bundle.Envelopes {
		if len(token) == 0 || len(token) > auth.MaxSignedBytes {
			return OfflineBundle{}, ErrOfflineBundle
		}
	}
	return bundle, nil
}
