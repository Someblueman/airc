package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

func sortedRules(rules map[string]protocol.ModerationRule, now time.Time) []protocol.ModerationRule {
	result := make([]protocol.ModerationRule, 0, len(rules))
	for _, rule := range rules {
		if ruleActive(rule, now) {
			result = append(result, rule)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return ruleKey(result[i].Kind, result[i].Nick, result[i].Scope) < ruleKey(result[j].Kind, result[j].Nick, result[j].Scope)
	})
	return result
}

// RestoreModeration must run after EnableAdmin and before Serve. A malformed
// file is fatal rather than silently dropping restrictions.
func (s *Server) RestoreModeration(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.adminEnabled || s.listener != nil || s.moderationAt != "" {
		return errors.New("configure moderation once after EnableAdmin, before Serve")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	loaded := make(map[string]protocol.ModerationRule)
	if err == nil {
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		if err != nil {
			return err
		}
		if len(data) > 4<<20 {
			return errors.New("moderation file exceeds 4 MiB")
		}
		var rules []protocol.ModerationRule
		if err := decodeStrict(bytes.NewReader(data), &rules); err != nil {
			return fmt.Errorf("read moderation: %w", err)
		}
		if rules == nil || len(rules) > maxModerationRules {
			return errors.New("moderation file must contain one bounded JSON array")
		}
		for _, rule := range rules {
			if rule.Kind != "mute" && rule.Kind != "ban" || validateAdminRequest(protocol.AdminRequest{Action: rule.Kind, Nick: rule.Nick, Scope: rule.Scope, Reason: rule.Reason}) != nil || rule.Scope == "" || !validNick(rule.SetBy) || rule.SetAt.IsZero() || !rule.ExpiresAt.IsZero() && (!rule.ExpiresAt.After(rule.SetAt) || rule.ExpiresAt.Sub(rule.SetAt) > 30*24*time.Hour) {
				return errors.New("invalid saved moderation rule")
			}
			key := ruleKey(rule.Kind, rule.Nick, rule.Scope)
			if _, duplicate := loaded[key]; duplicate {
				return errors.New("duplicate saved moderation rule")
			}
			loaded[key] = rule
		}
	}
	s.moderation, s.moderationAt = loaded, path
	return nil
}

func (s *Server) saveModerationLocked(next map[string]protocol.ModerationRule) error {
	if s.moderationAt == "" {
		return nil
	}
	data, err := json.Marshal(sortedRules(next, s.now()))
	if err != nil {
		return err
	}
	return atomicfile.Write(s.moderationAt, append(data, '\n'), 0o600, s.cfg.Sync)
}
