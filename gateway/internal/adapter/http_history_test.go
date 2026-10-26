package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Someblueman/airc/gateway/internal/core"
)

func TestHTTPConversationQueryFields(t *testing.T) {
	for _, path := range []string{"/v1/history", "/v1/search", "/v1/watch", "/v1/events"} {
		r := httptest.NewRequest("GET", path+"?kind=room&target=%23Room&parent_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&thread_mode=replies&limit=2"+queryFor(path), nil)
		body, start, err := readRequest(r)
		if err != nil || start != "" && path != "/v1/history" && path != "/v1/watch" && path != "/v1/events" {
			t.Fatalf("%s query: %v start=%q", path, err, start)
		}
		var request core.Request
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.ParentID == "" || request.ThreadMode != "replies" || request.Target.Name != "#Room" {
			t.Fatalf("%s request lost conversation selector: %+v", path, request)
		}
	}
	for _, raw := range []string{
		"/v1/history?kind=room&target=%23Room&parent_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa&unknown=x",
	} {
		if _, _, err := readRequest(httptest.NewRequest("GET", raw, nil)); err == nil {
			t.Fatalf("accepted unknown conversation query field: %s", raw)
		}
	}
}

func TestHTTPMembersQueryFields(t *testing.T) {
	body, start, err := readRequest(httptest.NewRequest("GET", "/v1/members?kind=room&target=%23Room", nil))
	if err != nil || start != "" {
		t.Fatalf("members query: %v start=%q", err, start)
	}
	var request core.Request
	if err := json.Unmarshal(body, &request); err != nil {
		t.Fatal(err)
	}
	if request.Operation != core.MembersRead || request.Target == nil || request.Target.Kind != "room" || request.Target.Name != "#Room" {
		t.Fatalf("members request = %+v", request)
	}
	for _, raw := range []string{
		"/v1/members?kind=room&target=%23Room&limit=2",
		"/v1/members?kind=room&target=%23Room&parent_id=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	} {
		if _, _, err := readRequest(httptest.NewRequest("GET", raw, nil)); err == nil {
			t.Fatalf("accepted invalid members query field: %s", raw)
		}
	}
}

func TestHTTPLiveQueriesSelectEventsWatch(t *testing.T) {
	for _, path := range []string{"/v1/live", "/v1/live/events"} {
		body, start, err := readRequest(httptest.NewRequest("GET", path+"?kind=room&target=%23Room&limit=2", nil))
		if err != nil || start != "" {
			t.Fatalf("%s query: %v start=%q", path, err, start)
		}
		var request core.Request
		if err := json.Unmarshal(body, &request); err != nil {
			t.Fatal(err)
		}
		if request.Operation != core.EventsWatch || request.Target == nil || request.Target.Name != "#Room" {
			t.Fatalf("%s request = %+v", path, request)
		}
	}
}

func queryFor(path string) string {
	if path == "/v1/search" {
		return "&query=body"
	}
	return ""
}
