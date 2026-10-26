package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The files under --state-dir have fixed names. Credential files, the
// service manifest and the logs are the installer's and always named by
// their own flags.
const (
	stateHistory    = "history.jsonl"
	stateAccounts   = "accounts.json"
	stateChat       = "chat.json"
	stateHandoffs   = "handoffs.jsonl"
	stateDecisions  = "decisions.jsonl"
	stateWakes      = "wakes.json"
	stateTopics     = "topics.json"
	stateProfiles   = "profiles.json"
	stateModeration = "moderation.json"
	// statePastes is the paste index; the pastes themselves are files in
	// the directory named after it (pastes/). There is no --paste-file:
	// pastes live under --state-dir or beside the history file.
	statePastes = "pastes.json"
)

// statePaths are the daemon's state files, one per feature: "" leaves
// that feature in memory. As flags they are the explicit --*-file
// overrides, each of which wins over --state-dir and the older derivations.
type statePaths struct {
	history, accounts, chat, handoffs, decisions, wakes, topics, profiles, moderation, pastes string
}

// stateNote is something resolveState found worth logging, once the logger
// exists: a state file still at its older name, or shadowed by the fixed one.
type stateNote struct {
	event, path, canonical string
}

// resolveState chooses every state file. An explicit flag wins. Without
// --state-dir the defaults are the older derivations next to the history
// file (or the admin token), so no existing invocation changes. With it,
// each file is DIR/<fixed name>; a file still at its older name is used
// where the fixed one does not exist yet, so nothing is renamed, and
// reported in notes. wake says whether wake notices are on, which need the
// accounts file.
func resolveState(dir string, flags statePaths, adminToken string, wake bool) (statePaths, []stateNote, error) {
	paths := statePaths{history: flags.history}
	if dir != "" && paths.history == "" {
		paths.history = filepath.Join(dir, stateHistory)
	}
	var notes []stateNote
	// choose resolves one file from its override and its older derivation,
	// which is derived from the files resolved before it.
	choose := func(override, legacy, name string) (string, error) {
		if override != "" {
			return override, nil
		}
		if dir == "" {
			return legacy, nil
		}
		canonical := filepath.Join(dir, name)
		canonicalExists, err := fileExists(canonical)
		if err != nil {
			return "", err
		}
		legacyExists := false
		if legacy != "" && legacy != canonical {
			if legacyExists, err = fileExists(legacy); err != nil {
				return "", err
			}
		}
		switch {
		case canonicalExists && legacyExists:
			notes = append(notes, stateNote{event: "state_file_shadowed", path: legacy, canonical: canonical})
		case legacyExists:
			notes = append(notes, stateNote{event: "state_file_legacy_name", path: legacy, canonical: canonical})
			return legacy, nil
		}
		return canonical, nil
	}
	// beside is the older derivation: a suffix on the history file, else on
	// the fallback, else nothing.
	beside := func(fallback, suffix string) string {
		if paths.history != "" {
			return paths.history + suffix
		}
		if fallback != "" {
			return fallback + suffix
		}
		return ""
	}
	var err error
	if paths.accounts, err = choose(flags.accounts, beside(adminToken, ".accounts.json"), stateAccounts); err != nil {
		return paths, nil, err
	}
	if paths.chat, err = choose(flags.chat, beside(paths.accounts, ".chat.json"), stateChat); err != nil {
		return paths, nil, err
	}
	if paths.handoffs, err = choose(flags.handoffs, beside(paths.accounts, ".handoffs.jsonl"), stateHandoffs); err != nil {
		return paths, nil, err
	}
	if paths.decisions, err = choose(flags.decisions, beside(paths.accounts, ".decisions.jsonl"), stateDecisions); err != nil {
		return paths, nil, err
	}
	if wake && paths.accounts != "" {
		legacy := paths.accounts + ".wakes.json"
		if paths.wakes, err = choose(flags.wakes, legacy, stateWakes); err != nil {
			return paths, nil, err
		}
	} else if wake {
		paths.wakes = flags.wakes
	}
	if paths.topics, err = choose(flags.topics, beside("", ".topics.json"), stateTopics); err != nil {
		return paths, nil, err
	}
	if paths.pastes, err = choose("", beside(paths.accounts, ".pastes.json"), statePastes); err != nil {
		return paths, nil, err
	}
	if paths.profiles, err = choose(flags.profiles, beside(paths.accounts, ".profiles.json"), stateProfiles); err != nil {
		return paths, nil, err
	}
	if adminToken != "" {
		if paths.moderation, err = choose(flags.moderation, beside(adminToken, ".moderation.json"), stateModeration); err != nil {
			return paths, nil, err
		}
	} else {
		paths.moderation = flags.moderation
	}
	return paths, notes, nil
}

// list is every resolved path, for the distinctness check; "" entries are
// features left in memory.
func (p statePaths) list() []string {
	return []string{p.history, p.accounts, p.chat, p.handoffs, p.decisions, p.wakes, p.topics, p.profiles, p.moderation, p.pastes}
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("check state file %s: %w", path, err)
}
