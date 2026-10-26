package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateKeepsTodaysDerivationsWithoutStateDir(t *testing.T) {
	history := filepath.Join(t.TempDir(), "history.jsonl")
	token := filepath.Join(t.TempDir(), "admin.token")
	for name, tc := range map[string]struct {
		flags statePaths
		token string
		wake  bool
		want  statePaths
	}{
		"no flags": {},
		"history file only": {
			flags: statePaths{history: history},
			want: statePaths{
				history: history, accounts: history + ".accounts.json", chat: history + ".chat.json",
				handoffs: history + ".handoffs.jsonl", decisions: history + ".decisions.jsonl",
				topics: history + ".topics.json", profiles: history + ".profiles.json",
			},
		},
		"history file and wake": {
			flags: statePaths{history: history}, wake: true,
			want: statePaths{
				history: history, accounts: history + ".accounts.json", chat: history + ".chat.json",
				handoffs: history + ".handoffs.jsonl", decisions: history + ".decisions.jsonl", wakes: history + ".accounts.json.wakes.json",
				topics: history + ".topics.json", profiles: history + ".profiles.json",
			},
		},
		"admin token only": {
			token: token,
			want: statePaths{
				accounts: token + ".accounts.json", chat: token + ".accounts.json.chat.json",
				handoffs: token + ".accounts.json.handoffs.jsonl", decisions: token + ".accounts.json.decisions.jsonl",
				profiles: token + ".accounts.json.profiles.json", moderation: token + ".moderation.json",
			},
		},
		"history file and admin token": {
			flags: statePaths{history: history}, token: token,
			want: statePaths{
				history: history, accounts: history + ".accounts.json", chat: history + ".chat.json",
				handoffs: history + ".handoffs.jsonl", decisions: history + ".decisions.jsonl",
				topics: history + ".topics.json", profiles: history + ".profiles.json", moderation: history + ".moderation.json",
			},
		},
		"explicit files win": {
			flags: statePaths{history: history, accounts: "/a", chat: "/c", handoffs: "/h", decisions: "/d", wakes: "/w", topics: "/t", profiles: "/p", moderation: "/m"},
			token: token, wake: true,
			want: statePaths{history: history, accounts: "/a", chat: "/c", handoffs: "/h", decisions: "/d", wakes: "/w", topics: "/t", profiles: "/p", moderation: "/m"},
		},
		"moderation file without a token is kept for configureAdmin to refuse": {
			flags: statePaths{moderation: "/m"},
			want:  statePaths{moderation: "/m"},
		},
	} {
		got, notes, err := resolveState("", tc.flags, tc.token, tc.wake)
		if err != nil || len(notes) != 0 || got != tc.want {
			t.Errorf("%s: got %+v, notes %v, err %v; want %+v", name, got, notes, err, tc.want)
		}
	}
}

func TestResolveStateUsesFixedNamesUnderStateDir(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "admin.token")
	history := filepath.Join(dir, "history.jsonl")
	canonical := statePaths{
		history: history, accounts: filepath.Join(dir, "accounts.json"), chat: filepath.Join(dir, "chat.json"),
		handoffs: filepath.Join(dir, "handoffs.jsonl"), decisions: filepath.Join(dir, "decisions.jsonl"), wakes: filepath.Join(dir, "wakes.json"),
		topics: filepath.Join(dir, "topics.json"), profiles: filepath.Join(dir, "profiles.json"), moderation: filepath.Join(dir, "moderation.json"),
	}
	got, notes, err := resolveState(dir, statePaths{}, token, true)
	if err != nil || len(notes) != 0 || got != canonical {
		t.Fatalf("fresh directory: got %+v, notes %v, err %v", got, notes, err)
	}

	// Without a token or wake notices, those files are not resolved.
	got, _, err = resolveState(dir, statePaths{}, "", false)
	plain := canonical
	plain.moderation, plain.wakes = "", ""
	if err != nil || got != plain {
		t.Fatalf("without admin and wake: got %+v, err %v", got, err)
	}

	// An explicit flag wins over the directory.
	got, notes, err = resolveState(dir, statePaths{history: "/elsewhere/h.jsonl", chat: "/c"}, token, false)
	if err != nil || len(notes) != 0 || got.history != "/elsewhere/h.jsonl" || got.chat != "/c" || got.accounts != canonical.accounts {
		t.Fatalf("overrides: got %+v, notes %v, err %v", got, notes, err)
	}
}

func TestResolveStateFindsLegacyFilesAndNeverRenamesThem(t *testing.T) {
	dir := t.TempDir()
	token := filepath.Join(dir, "admin.token")
	history := filepath.Join(dir, "history.jsonl")
	legacy := statePaths{
		accounts: history + ".accounts.json", chat: history + ".chat.json", handoffs: history + ".handoffs.jsonl",
		decisions: history + ".decisions.jsonl", wakes: history + ".accounts.json.wakes.json", topics: history + ".topics.json",
		profiles: history + ".profiles.json", moderation: history + ".moderation.json",
	}
	for _, path := range []string{history, legacy.accounts, legacy.chat, legacy.handoffs, legacy.decisions, legacy.wakes, legacy.topics, legacy.profiles, legacy.moderation} {
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, notes, err := resolveState(dir, statePaths{}, token, true)
	if err != nil {
		t.Fatal(err)
	}
	legacy.history = history
	if got != legacy {
		t.Fatalf("legacy layout: got %+v, want %+v", got, legacy)
	}
	if len(notes) != 8 {
		t.Fatalf("notes: %v", notes)
	}
	for _, note := range notes {
		if note.event != "state_file_legacy_name" || note.path == "" || filepath.Dir(note.canonical) != dir {
			t.Fatalf("note: %+v", note)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 9 {
		t.Fatalf("resolution changed the directory: %v", entries)
	}

	// A fixed-name file shadows the legacy one, with a warning.
	if err := os.WriteFile(filepath.Join(dir, "chat.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, notes, err = resolveState(dir, statePaths{}, token, true)
	if err != nil || got.chat != filepath.Join(dir, "chat.json") {
		t.Fatalf("shadowed: got %+v, err %v", got, err)
	}
	shadowed := 0
	for _, note := range notes {
		if note.event == "state_file_shadowed" {
			shadowed++
			if note.path != legacy.chat || note.canonical != got.chat {
				t.Fatalf("shadow note: %+v", note)
			}
		}
	}
	if shadowed != 1 || len(notes) != 8 {
		t.Fatalf("notes after shadowing: %v", notes)
	}

	// An explicit --history-file elsewhere moves the legacy derivations with it.
	other := filepath.Join(t.TempDir(), "old.jsonl")
	if err := os.WriteFile(other+".topics.json", []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err = resolveState(dir, statePaths{history: other}, "", false)
	if err != nil || got.history != other || got.topics != other+".topics.json" || got.accounts != filepath.Join(dir, "accounts.json") {
		t.Fatalf("history elsewhere: got %+v, err %v", got, err)
	}
}
