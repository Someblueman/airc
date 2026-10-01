package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
)

type account struct {
	ID   string `json:"id"`
	Nick string `json:"nick"`
	Hash string `json:"credential_hash"`
}

func (s *Server) RestoreAccounts(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.accountsAt != "" {
		return errors.New("configure accounts once before Serve")
	}
	loaded := map[string]account{}
	if err := readState(path, &loaded); err != nil {
		return err
	}
	if loaded == nil || len(loaded) > 1024 {
		return errors.New("invalid account store")
	}
	ids := map[string]bool{}
	for key, a := range loaded {
		if key != nickKey(a.Nick) || !validNick(a.Nick) || !protocol.ValidMessageID(a.ID) || !admin.ValidToken(a.Hash) || ids[a.ID] {
			return errors.New("invalid saved account")
		}
		ids[a.ID] = true
	}
	s.accounts, s.accountsAt = loaded, path
	return nil
}

func (s *Server) authLocked(client *session, command protocol.Command) {
	if !client.registered {
		client.accountID, client.authNick = "", ""
	}
	nick, _ := command.Param(0)
	if client.registered || s.accountsAt == "" || !validNick(nick) || !admin.ValidToken(command.Trailing) {
		s.numericLocked(client, "498", nil, "Invalid account authentication or accounts disabled")
		return
	}
	key := nickKey(nick)
	a, exists := s.accounts[key]
	hash := sha256.Sum256([]byte(command.Trailing))
	if !exists && command.Name == "REGISTER" {
		if len(s.accounts) >= 1024 {
			s.numericLocked(client, "437", nil, "Account limit reached")
			return
		}
		for _, other := range s.clients {
			if other != client && strings.EqualFold(other.client.Nick, nick) {
				s.numericLocked(client, "433", nil, "Nickname has an active guest session")
				return
			}
		}
		a = account{ID: newID(), Nick: nick, Hash: hex.EncodeToString(hash[:])}
		next := make(map[string]account, len(s.accounts)+1)
		for k, value := range s.accounts {
			next[k] = value
		}
		next[key] = a
		if err := writeState(s.accountsAt, next); err != nil {
			s.numericLocked(client, "437", nil, "Account not saved")
			return
		}
		s.accounts = next
		s.logger.Info("account_created", "nick", nick, "account_id", a.ID)
	} else if !exists {
		s.numericLocked(client, "498", nil, "Unknown account or invalid account credential")
		return
	}
	expected, _ := hex.DecodeString(a.Hash)
	if subtle.ConstantTimeCompare(hash[:], expected) != 1 {
		s.numericLocked(client, "498", nil, "Unknown account or invalid account credential")
		return
	}
	client.accountID, client.authNick = a.ID, key
	s.numericLocked(client, "779", []string{a.ID}, "Account authenticated")
}

func (s *Server) identityAllowedLocked(client *session, nick string) bool {
	a, reserved := s.accounts[nickKey(nick)]
	if reserved && (client.accountID != a.ID || client.authNick != nickKey(nick)) || client.accountID != "" && client.authNick != nickKey(nick) {
		s.numericLocked(client, "498", nil, "Nickname is registered; use its identity file")
		return false
	}
	return true
}
