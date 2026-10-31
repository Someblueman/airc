package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/admin"
	"github.com/Someblueman/airc/internal/protocol"
)

const (
	maxAccounts = 1024
	// maxRegistrationsPerConnection keeps one client from filling the account
	// namespace in a single session; an admin can delete accounts afterwards.
	maxRegistrationsPerConnection = 1
	// A remote address may create this many accounts per window, so
	// reconnecting does not get around the per-connection limit. Loopback and
	// Unix-socket peers are exempt, like the unregistered-connection cap,
	// unless a gateway asserted their real source with WEBIRC.
	maxRegistrationsPerAddress = 8
	registrationWindow         = time.Hour
)

type account struct {
	ID   string `json:"id"`
	Nick string `json:"nick"`
	Hash string `json:"credential_hash"`
}

// accountsState is the registered users: the accounts by nickname key, the
// file they are saved to and the recent creations by remote address. Every
// field is read and written with s.mu held; saves write before the caller
// applies, under s.mu, with env.sync.
type accountsState struct {
	accounts      map[string]account
	accountsAt    string
	registrations map[string][]time.Time // recent account creations by remote address
	env           *storeEnv
}

func (s *Server) RestoreAccounts(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.accountsAt != "" {
		return errors.New("configure accounts once before Serve")
	}
	return s.accountsState.load(path)
}

func (st *accountsState) load(path string) error {
	loaded := map[string]account{}
	if err := readState(path, &loaded); err != nil {
		return err
	}
	if loaded == nil || len(loaded) > maxAccounts {
		return errors.New("invalid account store")
	}
	ids := map[string]bool{}
	for key, a := range loaded {
		if key != nickKey(a.Nick) || !protocol.ValidNick(a.Nick) || !protocol.ValidMessageID(a.ID) || !admin.ValidToken(a.Hash) || ids[a.ID] {
			return errors.New("invalid saved account")
		}
		ids[a.ID] = true
	}
	st.accounts, st.accountsAt = loaded, path
	return nil
}

func (st *accountsState) close() error { return nil }

// saveAccountsLocked writes next, the accounts as they will be, before the
// caller applies it.
func (st *accountsState) saveAccountsLocked(next map[string]account) error {
	return st.env.saved("accounts", writeState(st.accountsAt, next, st.env.sync))
}

// accountExistsLocked is false once an admin has deleted the account, which
// ends any operator grant it held without rewriting the chat snapshot.
func (st *accountsState) accountExistsLocked(id string) bool {
	for _, a := range st.accounts {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (s *Server) authLocked(client *session, command protocol.Command) {
	if !client.registered {
		client.accountID, client.authNick = "", ""
	}
	if s.credentialThrottledLocked(client, "498") {
		return
	}
	nick, _ := command.Param(0)
	if client.registered || s.accountsAt == "" || !protocol.ValidNick(nick) || !admin.ValidToken(command.Trailing) {
		s.credentialFailedLocked(client, "498", nil, "Invalid account authentication or accounts disabled")
		return
	}
	key := nickKey(nick)
	a, exists := s.accounts[key]
	hash := sha256.Sum256([]byte(command.Trailing))
	if !exists && command.Name == "REGISTER" {
		// The creation windows are 437, since a new connection or a later
		// hour may succeed; the account limit is 405 like every limit a
		// retry cannot clear. Clients abort their registration on both, so
		// neither falls through to a guest session under the name.
		if client.accountsMade >= maxRegistrationsPerConnection {
			s.numericLocked(client, "437", nil, "Account creation limit for this connection reached")
			return
		}
		if len(s.accounts) >= maxAccounts {
			s.numericLocked(client, "405", nil, "Account limit reached")
			return
		}
		if !s.registrationAllowedLocked(client.remoteKey) {
			s.numericLocked(client, "437", nil, "Account creation limit for this address reached; try again later")
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
		maps.Copy(next, s.accounts)
		next[key] = a
		if err := s.saveAccountsLocked(next); err != nil {
			s.numericLocked(client, "437", nil, "Account not saved")
			return
		}
		s.accounts = next
		client.accountsMade++
		s.noteRegistrationLocked(client.remoteKey)
		s.logger.Info("account_created", "nick", nick, "account_id", a.ID)
	} else if !exists {
		s.credentialFailedLocked(client, "498", nil, "Unknown account or invalid account credential")
		return
	}
	expected, _ := hex.DecodeString(a.Hash)
	if subtle.ConstantTimeCompare(hash[:], expected) != 1 {
		s.credentialFailedLocked(client, "498", nil, "Unknown account or invalid account credential")
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

func sortedAccounts(accounts map[string]account) []account {
	list := make([]account, 0, len(accounts))
	for _, a := range accounts {
		list = append(list, a)
	}
	slices.SortFunc(list, func(a, b account) int { return strings.Compare(nickKey(a.Nick), nickKey(b.Nick)) })
	return list
}

// deleteAccountLocked removes an account so its nickname can be registered
// again, by anyone, with a new account ID. Like other account writes it saves
// before applying. Connections stay open: a live session keeps its identity
// for authorship but loses account-based privileges (see channelOperator), and
// the old ID is never reused, so nothing it authored can be claimed later.
func (s *Server) deleteAccountLocked(client *session, request protocol.AdminRequest) {
	result := protocol.AdminResult{Action: request.Action, Nick: request.Nick}
	key := nickKey(request.Nick)
	if a, exists := s.accounts[key]; exists {
		// A room it orchestrates stops being orchestrated first, so the chat
		// snapshot never names an orchestrator without an account.
		if err := s.clearOrchestratorLocked(a.Nick); err != nil {
			s.logger.Error("chat_write_failed", "error", err.Error())
			s.numericLocked(client, "437", nil, "Unable to save chat state; no change applied")
			return
		}
		next := make(map[string]account, len(s.accounts))
		for k, value := range s.accounts {
			if k != key {
				next[k] = value
			}
		}
		if err := s.saveAccountsLocked(next); err != nil {
			s.logger.Error("account_write_failed", "error", err.Error())
			s.numericLocked(client, "437", nil, "Unable to save accounts; no change applied")
			return
		}
		s.accounts = next
		if w := s.wake; w != nil && w.byAccount[a.ID] != nil {
			delete(w.byAccount, a.ID)
			if err := w.saveWakesLocked(w.byAccount); err != nil {
				s.logger.Error("wake_save_failed", "error", err.Error())
			}
		}
		result.Nick, result.AccountID, result.Changed = a.Nick, a.ID, true
	}
	s.logger.Info("admin_action", "actor", client.client.Nick, "action", request.Action, "nick", request.Nick, "account_id", result.AccountID, "changed", result.Changed)
	s.adminResultLocked(client, result)
	s.numericLocked(client, "776", nil, "End of admin result")
}

// recentRegistrationsLocked drops creations older than the window and returns
// what remains for key.
func (st *accountsState) recentRegistrationsLocked(key string) []time.Time {
	return recentEvents(st.registrations, key, st.env.now().Add(-registrationWindow))
}

// recentEvents drops times in table[key] that are not after cutoff, deleting
// an emptied entry, and returns what remains.
func recentEvents(table map[string][]time.Time, key string, cutoff time.Time) []time.Time {
	recent := table[key]
	for len(recent) > 0 && !recent[0].After(cutoff) {
		recent = recent[1:]
	}
	if len(recent) == 0 {
		delete(table, key)
	} else {
		table[key] = recent
	}
	return recent
}

func (st *accountsState) registrationAllowedLocked(key string) bool {
	return key == "" || len(st.recentRegistrationsLocked(key)) < maxRegistrationsPerAddress
}

func (st *accountsState) noteRegistrationLocked(key string) {
	if key == "" {
		return
	}
	if len(st.registrations) >= maxAccounts {
		// At most maxAccounts creations can ever be live, so a full table
		// holds only expired addresses.
		for other := range st.registrations {
			st.recentRegistrationsLocked(other)
		}
	}
	st.registrations[key] = append(st.registrations[key], st.env.now())
}
