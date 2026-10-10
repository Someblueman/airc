package core

import "testing"

func TestCapabilityDiscoveryArguments(t *testing.T) {
	r := Request{Version: 1, RequestID: "discover", Operation: CapabilitiesGet}
	if err := r.Normalize(); err != nil {
		t.Fatal(err)
	}
	if CapabilitiesGet.Valid() {
		t.Fatal("discovery must not become a configurable AIRC permission")
	}
	for _, change := range []func(*Request){
		func(r *Request) { r.Target = &Target{Kind: "room", Name: "#room"} },
		func(r *Request) { r.LookupID = "other" },
		func(r *Request) { r.Cursor = "cursor" },
		func(r *Request) { r.Query = "query" },
		func(r *Request) { r.From = "sender" },
		func(r *Request) { r.Limit = 1 },
		func(r *Request) { r.Message = "body" },
		func(r *Request) { i := int64(1); r.IssuedAt = &i },
	} {
		bad := r
		change(&bad)
		if err := bad.Normalize(); err == nil {
			t.Fatal("discovery accepted operation arguments", bad)
		}
	}
}
