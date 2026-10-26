package protocol

import "testing"

func TestStatusAndResolvedText(t *testing.T) {
	for _, c := range []struct {
		state, task, note string
		want              string
	}{
		{"working", "port the fix", "", "Status [working] port the fix"},
		{"done", "port the fix", "merged in #54", "Status [done] port the fix: merged in #54"},
		{"blocked", "port the fix", "CI is red", "Status [blocked] port the fix: CI is red"},
	} {
		if got := StatusText(c.state, c.task, c.note); got != c.want {
			t.Errorf("StatusText(%q, %q, %q) = %q, want %q", c.state, c.task, c.note, got, c.want)
		}
	}
	if got := ResolvedText(""); got != "Resolved" {
		t.Errorf("ResolvedText(\"\") = %q", got)
	}
	if got := ResolvedText("settled in the DM"); got != "settled in the DM" {
		t.Errorf("ResolvedText(note) = %q", got)
	}
	for state, want := range map[string]bool{"working": true, "blocked": true, "done": true, "Working": false, "idle": false, "": false} {
		if got := ValidStatusState(state); got != want {
			t.Errorf("ValidStatusState(%q) = %v, want %v", state, got, want)
		}
	}
}

// A status post's task, state and link, and a root's resolved mark, travel
// in the chat tag with the rest of the metadata.
func TestStatusMetadataRoundTrips(t *testing.T) {
	meta := ChatMetadata{Kind: "status", Task: "port the fix", State: "working", Link: "https://example.com/pr/1"}
	if got := DecodeChat(EncodeChat(meta)); got.Kind != "status" || got.Task != meta.Task || got.State != meta.State || got.Link != meta.Link {
		t.Fatalf("decoded %+v", got)
	}
	if got := DecodeChat(EncodeChat(ChatMetadata{Resolved: true})); !got.Resolved {
		t.Fatalf("decoded %+v", got)
	}
	if got := DecodeChat(EncodeChat(ChatMetadata{})); got.Resolved || got.Task != "" || got.State != "" {
		t.Fatalf("empty metadata decoded %+v", got)
	}
}
