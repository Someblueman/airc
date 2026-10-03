package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
)

const maxModerationRules = 1024
const maxModerationSeconds = int64(30 * 24 * 60 * 60)

// EnableAdmin configures the operator credential before Serve. No nickname or
// self-reported profile confers privileges. Reconfigure by restarting the server.
func (s *Server) EnableAdmin(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.adminEnabled {
		return errors.New("admin must be configured once, before Serve")
	}
	if !admin.ValidToken(token) {
		return errors.New("invalid admin credential")
	}
	if s.accessEnabled && sha256.Sum256([]byte(token)) == s.accessHash {
		return errors.New("admin and access credentials must be different")
	}
	s.adminHash, s.adminEnabled = sha256.Sum256([]byte(token)), true
	s.moderation = make(map[string]protocol.ModerationRule)
	return nil
}

func (s *Server) operLocked(client *session, command protocol.Command) {
	hash := sha256.Sum256([]byte(command.Trailing))
	client.admin = s.adminEnabled && subtle.ConstantTimeCompare(hash[:], s.adminHash[:]) == 1
	if !client.admin {
		s.credentialFailedLocked(client, "464", nil, "Invalid admin credential or administration disabled")
		return
	}
	s.numericLocked(client, "381", nil, "Admin authenticated for this connection")
}

func ruleKey(kind, nick, scope string) string { return kind + ":" + nickKey(nick) + ":" + scope }

func ruleActive(rule protocol.ModerationRule, now time.Time) bool {
	return rule.ExpiresAt.IsZero() || now.Before(rule.ExpiresAt)
}

func (s *Server) restrictionLocked(kind, nick, scope string) (protocol.ModerationRule, bool) {
	now := time.Now()
	for _, where := range []string{"*", scope} {
		rule, ok := s.moderation[ruleKey(kind, nick, where)]
		if ok && ruleActive(rule, now) {
			return rule, true
		}
	}
	return protocol.ModerationRule{}, false
}

func (s *Server) channelAllowedLocked(client *session, channel string) bool {
	if rule, banned := s.restrictionLocked("ban", client.client.Nick, channel); banned {
		s.numericLocked(client, "474", []string{channel}, restrictionText("Banned", rule))
		return false
	}
	return true
}

func restrictionText(label string, rule protocol.ModerationRule) string {
	text := label + " in " + rule.Scope
	if !rule.ExpiresAt.IsZero() {
		text += " until " + rule.ExpiresAt.Format(time.RFC3339)
	}
	if rule.Reason != "" {
		text += ": " + rule.Reason
	}
	return text
}

func (s *Server) postAllowedLocked(client *session, target string) bool {
	if isChannelName(target) && !s.channelAllowedLocked(client, target) {
		return false
	}
	if rule, muted := s.restrictionLocked("mute", client.client.Nick, target); muted {
		s.numericLocked(client, "485", []string{target}, restrictionText("Muted", rule))
		return false
	}
	return true
}

func (s *Server) adminLocked(client *session, command protocol.Command) {
	if !client.admin {
		s.numericLocked(client, "481", nil, "Authenticate with OPER before using ADMIN")
		return
	}
	var request protocol.AdminRequest
	decoder := json.NewDecoder(strings.NewReader(command.Trailing))
	decoder.DisallowUnknownFields()
	if len(command.Trailing) > 4096 || decoder.Decode(&request) != nil || !onlyJSONEnd(decoder) {
		s.numericLocked(client, "461", nil, "ADMIN requires one valid JSON request")
		return
	}
	if request.Scope == "" {
		request.Scope = "*"
	}
	if err := validateAdminRequest(request); err != nil {
		s.numericLocked(client, "461", nil, err.Error())
		return
	}
	if strings.HasPrefix(request.Action, "account-") && s.accountsAt == "" {
		s.numericLocked(client, "437", nil, "Accounts are not enabled on this daemon")
		return
	}
	if request.Action == "account-list" {
		for _, a := range sortedAccounts(s.accounts) {
			s.adminResultLocked(client, protocol.AdminResult{Action: request.Action, Nick: a.Nick, AccountID: a.ID})
		}
		s.numericLocked(client, "776", nil, "End of account list")
		return
	}
	if request.Action == "account-delete" {
		s.deleteAccountLocked(client, request)
		return
	}
	if request.Action == "list" {
		for _, rule := range sortedRules(s.moderation, time.Now()) {
			s.adminResultLocked(client, protocol.AdminResult{Action: "list", Rule: &rule})
		}
		s.numericLocked(client, "776", nil, "End of moderation list")
		return
	}
	if (request.Action == "kick" || request.Action == "ban") && strings.EqualFold(client.client.Nick, request.Nick) {
		s.numericLocked(client, "461", nil, "Use a different admin nickname when moderating your own nickname")
		return
	}
	result := protocol.AdminResult{Action: request.Action, Nick: request.Nick, Scope: request.Scope}
	if request.Action != "kick" {
		now := time.Now().UTC()
		next := make(map[string]protocol.ModerationRule)
		for key, rule := range s.moderation {
			if ruleActive(rule, now) {
				next[key] = rule
			}
		}
		kind := strings.TrimPrefix(request.Action, "un")
		key := ruleKey(kind, request.Nick, request.Scope)
		if strings.HasPrefix(request.Action, "un") {
			_, result.Changed = next[key]
			delete(next, key)
		} else {
			if _, exists := next[key]; !exists && len(next) >= maxModerationRules {
				s.numericLocked(client, "437", nil, "Moderation rule limit reached")
				return
			}
			rule := protocol.ModerationRule{Kind: kind, Nick: request.Nick, Scope: request.Scope, Reason: request.Reason, SetBy: client.client.Nick, SetAt: now}
			if request.Seconds > 0 {
				rule.ExpiresAt = now.Add(time.Duration(request.Seconds) * time.Second)
			}
			next[key], result.Rule, result.Changed = rule, &rule, true
		}
		if err := s.saveModerationLocked(next); err != nil {
			s.logger.Error("moderation_write_failed", "error", err.Error())
			s.numericLocked(client, "437", nil, "Unable to save moderation; no change applied")
			return
		}
		s.moderation = next
	}
	if request.Action == "kick" || request.Action == "ban" {
		for _, target := range s.clients {
			if strings.EqualFold(target.client.Nick, request.Nick) {
				select {
				case <-target.done:
					continue
				default:
				}
				target.quitReason = "Admin " + request.Action
				if request.Reason != "" {
					target.quitReason += ": " + request.Reason
				}
				target.close()
				result.Kicked++
			}
		}
	}
	s.logger.Info("admin_action", "actor", client.client.Nick, "action", request.Action, "nick", request.Nick, "scope", request.Scope, "reason", request.Reason, "changed", result.Changed, "kicked", result.Kicked)
	s.adminResultLocked(client, result)
	s.numericLocked(client, "776", nil, "End of admin result")
}

func validateAdminRequest(r protocol.AdminRequest) error {
	if r.Action == "list" || r.Action == "account-list" {
		if r.Nick != "" || r.Scope != "*" || r.Seconds != 0 || r.Reason != "" {
			return errors.New(r.Action + " does not accept a nickname, scope, duration or reason")
		}
		return nil
	}
	if r.Action == "account-delete" {
		if !validNick(r.Nick) {
			return errors.New("account-delete requires a valid nickname")
		}
		if r.Scope != "*" || r.Seconds != 0 || r.Reason != "" {
			return errors.New("account-delete accepts only a nickname")
		}
		return nil
	}
	switch r.Action {
	case "mute", "unmute", "ban", "unban", "kick":
	default:
		return errors.New("unknown admin action")
	}
	if !validNick(r.Nick) {
		return errors.New("admin requires a valid nickname")
	}
	if r.Scope != "*" && !validChannel(r.Scope) {
		return errors.New("scope must be * or a channel")
	}
	if !protocol.BriefText(r.Reason, 400) {
		return errors.New("reason must be valid text up to 400 bytes")
	}
	if r.Seconds < 0 || r.Seconds > maxModerationSeconds {
		return errors.New("duration must be 0 (indefinite) or 1s-30d")
	}
	if r.Action != "mute" && r.Action != "ban" && r.Seconds != 0 {
		return errors.New("only mute and ban accept a duration")
	}
	if (r.Action == "unmute" || r.Action == "unban") && r.Reason != "" {
		return errors.New("unmute and unban do not accept a reason")
	}
	if r.Action == "kick" && r.Scope != "*" {
		return errors.New("kick disconnects a nickname's sessions and has no channel scope")
	}
	return nil
}

func (s *Server) adminResultLocked(client *session, result protocol.AdminResult) {
	data, _ := json.Marshal(result)
	s.numericLocked(client, "775", nil, string(data))
}

// A channel-scoped ban also covers thread and direct-reply subscriptions. The
// root may have been evicted, but retained descendants still carry its target.
func (s *Server) conversationChannelLocked(target string) string {
	id, replies, ok := protocol.ConversationTarget(target)
	if !ok {
		return target
	}
	if message, found := s.history.message(id); found {
		return message.Target
	}
	for i := 0; i < s.history.size; i++ {
		message := s.history.at(i)
		if !replies && message.ThreadID == id || replies && message.ReplyTo == id {
			return message.Target
		}
	}
	return ""
}
