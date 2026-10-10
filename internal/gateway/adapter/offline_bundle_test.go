package adapter

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Someblueman/airc/internal/gateway/auth"
)

func TestOfflineBundleFormatBounds(t *testing.T) {
	for _, count := range []int{1, MaxOfflineBundleEntries} {
		bundle := OfflineBundle{Version: 1, Envelopes: make([]string, count)}
		for i := range bundle.Envelopes {
			bundle.Envelopes[i] = strings.Repeat("x", auth.MaxSignedBytes)
		}
		data, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		// All maximum-length tokens exceed the whole-file bound together.
		if count == MaxOfflineBundleEntries {
			if _, err := DecodeOfflineBundle(data); err == nil {
				t.Fatal("accepted oversized total bundle")
			}
			for i := range bundle.Envelopes {
				bundle.Envelopes[i] = "opaque-token"
			}
			data, _ = json.Marshal(bundle)
		}
		got, err := DecodeOfflineBundle(data)
		if err != nil || len(got.Envelopes) != count || got.Envelopes[0] != bundle.Envelopes[0] {
			t.Fatal("valid boundary bundle", got, err)
		}
	}
	base := []byte(`{"version":1,"envelopes":["opaque-token"]}`)
	exact := append(append([]byte(nil), base...), []byte(strings.Repeat(" ", MaxOfflineBundleBytes-len(base)))...)
	if _, err := DecodeOfflineBundle(exact); err != nil {
		t.Fatal("exact byte limit", err)
	}
	if _, err := DecodeOfflineBundle(append(exact, ' ')); err == nil {
		t.Fatal("accepted byte limit plus one")
	}
}

func TestOfflineBundleMalformedRefusals(t *testing.T) {
	entries := make([]string, MaxOfflineBundleEntries+1)
	for i := range entries {
		entries[i] = "opaque-token"
	}
	tooMany, _ := json.Marshal(OfflineBundle{Version: 1, Envelopes: entries})
	tooLong, _ := json.Marshal(OfflineBundle{Version: 1, Envelopes: []string{strings.Repeat("x", auth.MaxSignedBytes+1)}})
	for _, data := range [][]byte{
		nil, []byte(`{}`), []byte(`null`), []byte(`[]`),
		[]byte(`{"version":2,"envelopes":["token"]}`),
		[]byte(`{"version":1,"envelopes":[]}`),
		[]byte(`{"version":1,"envelopes":[""]}`),
		[]byte(`{"version":1,"envelopes":[null]}`),
		[]byte(`{"version":1,"envelopes":[1]}`),
		[]byte(`{"version":1,"version":1,"envelopes":["token"]}`),
		[]byte(`{"version":1,"Envelopes":["token"]}`),
		[]byte(`{"version":1,"envelopes":["token"],"attachments":[]}`),
		[]byte(`{"version":1,"envelopes":["token"]} {}`),
		[]byte("{\"version\":1,\"envelopes\":[\"\xff\"]}"), tooMany, tooLong,
	} {
		if _, err := DecodeOfflineBundle(data); err == nil {
			t.Fatal("accepted malformed bundle")
		}
	}
}
