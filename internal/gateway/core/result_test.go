package core

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestCapabilityIntersectionDeniesAbsentPermissions(t *testing.T) {
	all := []Operation{Send, Reply, ReceiptGet, HistoryRead, Watch, Search, Directory}
	if len(EffectiveOperations(all, all, nil, all)) != 0 {
		t.Fatal("missing permission became an allowed operation")
	}
	if len(EffectiveOperations(nil, all, all, all)) != 0 {
		t.Fatal("advertised unimplemented operation")
	}
	got := EffectiveOperations(append(all, Send, Operation("raw")), []Operation{Send, Reply, Watch}, all, []Operation{Send, Watch})
	if !slices.Equal(got, []Operation{Send, Watch}) {
		t.Fatalf("incorrect intersection: %v", got)
	}
}

func TestUnknownReceiptFactsRemainUnknown(t *testing.T) {
	data, err := json.Marshal(Result{Version: 1, RequestID: "r", State: Queued, Durable: true})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"accepted", "persisted", "recipient_connected"} {
		if string(fields[name]) != "null" {
			t.Fatalf("unknown %s became %s", name, fields[name])
		}
	}
	no := false
	data, _ = json.Marshal(Result{Version: 1, RequestID: "r", State: Accepted, Accepted: &no})
	_ = json.Unmarshal(data, &fields)
	if string(fields["accepted"]) != "false" {
		t.Fatal("explicit false lost")
	}
}
