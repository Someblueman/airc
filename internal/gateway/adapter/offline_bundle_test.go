package adapter

import (
	"encoding/json"
	"strings"
	"testing"
)

const offlineTestRequest = `{"version":1,"request_id":"r","operation":"send","target":{"kind":"room","name":"#fixture"},"message":"m","author":{"nick":"field"}}`

func offlineRequests(count int, request string) []json.RawMessage {
	requests := make([]json.RawMessage, count)
	for i := range requests {
		requests[i] = json.RawMessage(request)
	}
	return requests
}

func TestOfflineBundleFormatBounds(t *testing.T) {
	large := `{"message":"` + strings.Repeat("x", MaxRequestFrameBytes-14) + `"}`
	for _, count := range []int{1, MaxOfflineBundleEntries} {
		bundle := OfflineBundle{Version: 1, Requests: offlineRequests(count, large)}
		data, err := json.Marshal(bundle)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeOfflineBundle(data)
		if err != nil || len(got.Requests) != count || string(got.Requests[0]) != string(bundle.Requests[0]) {
			t.Fatal("valid boundary bundle", got, err)
		}
	}
	base := []byte(`{"version":1,"requests":[` + offlineTestRequest + `]}`)
	exact := append(append([]byte(nil), base...), []byte(strings.Repeat(" ", MaxOfflineBundleBytes-len(base)))...)
	if _, err := DecodeOfflineBundle(exact); err != nil {
		t.Fatal("exact byte limit", err)
	}
	if _, err := DecodeOfflineBundle(append(exact, ' ')); err == nil {
		t.Fatal("accepted byte limit plus one")
	}
}

func TestOfflineBundleMalformedRefusals(t *testing.T) {
	tooMany, _ := json.Marshal(OfflineBundle{Version: 1, Requests: offlineRequests(MaxOfflineBundleEntries+1, offlineTestRequest)})
	tooLong, _ := json.Marshal(OfflineBundle{Version: 1, Requests: offlineRequests(1, `{"message":"`+strings.Repeat("x", MaxRequestFrameBytes)+`"}`)})
	for _, data := range [][]byte{
		nil, []byte(`{}`), []byte(`null`), []byte(`[]`),
		[]byte(`{"version":2,"requests":[{}]}`),
		[]byte(`{"version":1,"requests":[]}`),
		[]byte(`{"version":1,"requests":[""]}`),
		[]byte(`{"version":1,"requests":[null]}`),
		[]byte(`{"version":1,"requests":[1]}`),
		[]byte(`{"version":1,"version":1,"requests":[{}]}`),
		[]byte(`{"version":1,"envelopes":["token"]}`),
		[]byte(`{"version":1,"requests":[{}],"attachments":[]}`),
		[]byte(`{"version":1,"requests":[{}]} {}`),
		[]byte("{\"version\":1,\"requests\":[\"\xff\"]}"), tooMany, tooLong,
	} {
		if _, err := DecodeOfflineBundle(data); err == nil {
			t.Fatalf("accepted malformed bundle %.80q", data)
		}
	}
}
