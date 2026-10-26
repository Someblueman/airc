package protocol

import "testing"

func TestDecisionStepText(t *testing.T) {
	for _, c := range []struct {
		kind, offer  string
		choice       int
		option, note string
		want         string
	}{
		{"decision-answer", "id1", 2, "simple parser", "", "Decided 2 (simple parser)"},
		{"decision-answer", "id1", 2, "simple parser", "ship it", "Decided 2 (simple parser): ship it"},
		{"decision-answer", "id1", 0, "", "go with the simple one", "go with the simple one"},
		{"decision-withdraw", "id1", 0, "", "", "Withdrew decision id1"},
		{"decision-withdraw", "id1", 0, "", "no longer needed", "no longer needed"},
		{"decision", "id1", 0, "", "", ""},
	} {
		if got := DecisionStepText(c.kind, c.offer, c.choice, c.option, c.note); got != c.want {
			t.Errorf("DecisionStepText(%q, %q, %d, %q, %q) = %q, want %q", c.kind, c.offer, c.choice, c.option, c.note, got, c.want)
		}
	}
}

func TestValidKind(t *testing.T) {
	for kind, want := range map[string]bool{"": true, "human": true, "agent": true, "Human": false, "bot": false} {
		if got := ValidKind(kind); got != want {
			t.Errorf("ValidKind(%q) = %v, want %v", kind, got, want)
		}
	}
}

// A decision's options and an answer's choice travel in the chat tag with
// the rest of the metadata, and are absent from messages that have none.
func TestDecisionMetadataRoundTrips(t *testing.T) {
	meta := ChatMetadata{Kind: "decision", For: "sws", Choices: []string{"yes", "no"}}
	if got := DecodeChat(EncodeChat(meta)); got.Kind != "decision" || got.For != "sws" || len(got.Choices) != 2 || got.Choices[1] != "no" {
		t.Fatalf("decoded %+v", got)
	}
	answer := DecodeChat(EncodeChat(ChatMetadata{Kind: "decision-answer", Choice: 2}))
	if answer.Choice != 2 || answer.Choices != nil {
		t.Fatalf("decoded %+v", answer)
	}
	if plain := EncodeChat(ChatMetadata{Kind: "action"}); DecodeChat(plain).Choice != 0 {
		t.Fatal("a message without a choice decoded one")
	}
}
