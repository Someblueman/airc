package server

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/Someblueman/airc/internal/protocol"
)

// moderationState is the mutes and bans by rule key and the file they are
// saved to. Every field is read and written with s.mu held; saves run under
// s.mu with env.sync, and a failed save refuses the change.
type moderationState struct {
	moderation   map[string]protocol.ModerationRule
	moderationAt string
	env          *storeEnv
}

var moderationSnapshot = snapshot{limit: 4 << 20, tooLarge: "moderation file exceeds 4 MiB"}

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
	return s.moderationState.load(path)
}

func (st *moderationState) load(path string) error {
	data, found, err := moderationSnapshot.bytes(path)
	if err != nil {
		return err
	}
	loaded := make(map[string]protocol.ModerationRule)
	if found {
		var rules []protocol.ModerationRule
		if err := decodeStrict(bytes.NewReader(data), &rules); err != nil {
			return fmt.Errorf("read moderation: %w", err)
		}
		if rules == nil || len(rules) > maxModerationRules {
			return errors.New("moderation file must contain one bounded JSON array")
		}
		for _, rule := range rules {
			if !validRule(rule) {
				return errors.New("invalid saved moderation rule")
			}
			key := ruleKey(rule.Kind, rule.Nick, rule.Scope)
			if _, duplicate := loaded[key]; duplicate {
				return errors.New("duplicate saved moderation rule")
			}
			loaded[key] = rule
		}
	}
	st.moderation, st.moderationAt = loaded, path
	return nil
}

// validRule is a saved rule the daemon would have made itself.
func validRule(rule protocol.ModerationRule) bool {
	return (rule.Kind == "mute" || rule.Kind == "ban") && validateAdminRequest(protocol.AdminRequest{Action: rule.Kind, Nick: rule.Nick, Scope: rule.Scope, Reason: rule.Reason}) == nil && rule.Scope != "" && protocol.ValidNick(rule.SetBy) && !rule.SetAt.IsZero() && (rule.ExpiresAt.IsZero() || rule.ExpiresAt.After(rule.SetAt) && rule.ExpiresAt.Sub(rule.SetAt) <= 30*24*time.Hour)
}

func (st *moderationState) close() error { return nil }

func (st *moderationState) saveModerationLocked(next map[string]protocol.ModerationRule) error {
	return moderationSnapshot.write(st.moderationAt, sortedRules(next, st.env.now()), st.env.sync)
}

func (st *moderationState) restrictionLocked(kind, nick, scope string) (protocol.ModerationRule, bool) {
	if len(st.moderation) == 0 {
		return protocol.ModerationRule{}, false // the common case, checked per recipient
	}
	now := st.env.now()
	for _, where := range []string{"*", scope} {
		rule, ok := st.moderation[ruleKey(kind, nick, where)]
		if ok && ruleActive(rule, now) {
			return rule, true
		}
	}
	return protocol.ModerationRule{}, false
}
