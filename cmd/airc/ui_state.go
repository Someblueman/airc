package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/pkg/irc"
)

type uiDraft struct {
	Manual    bool   `json:"manual,omitempty"`
	Text      string `json:"text"`
	Cursor    int    `json:"cursor"`
	ReplyTo   string `json:"reply_to,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

type uiSavedState struct {
	Version int                `json:"version"`
	Current string             `json:"current"`
	Drafts  map[string]uiDraft `json:"drafts"`
}

type uiStateStore struct {
	path string
	lock *os.File
	last []byte
}

func (m *uiModel) rememberDraft() {
	if m.drafts == nil {
		m.drafts = map[string]uiDraft{}
	}
	if len(m.input) == 0 && m.uncertainID == "" && m.replyTo == "" {
		delete(m.drafts, m.current)
		return
	}
	m.drafts[m.current] = uiDraft{Text: string(m.input), Cursor: m.cursor, ReplyTo: m.replyTo, RequestID: m.uncertainID, Manual: m.uncertainAction || uncertainChat(m.pending)}
}

func (m *uiModel) restoreDraft(name string) {
	d := m.drafts[name]
	m.uncertainAction = d.Manual
	m.input, m.cursor, m.replyTo, m.uncertainID = []rune(d.Text), d.Cursor, d.ReplyTo, d.RequestID
	if d.Manual {
		m.setStatus("Unconfirmed chat action restored; inspect the conversation before discarding or trying again", true)
	}
	if d.RequestID != "" {
		m.setStatus("Unconfirmed send restored; Enter recovers its receipt without posting", true)
	}
}

func openUIState(opt options, m *uiModel) (*uiStateStore, error) {
	path, _, err := cursorPath(opt, opt.nick)
	if err != nil {
		return nil, err
	}
	path = filepath.Join(filepath.Dir(path), "ui-"+filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another UI is using this identity's drafts: %w", err)
	}
	s := &uiStateStore{path: path, lock: lock}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	var state uiSavedState
	if err == nil {
		err = json.Unmarshal(data, &state)
	}
	if err == nil && (len(data) > 8<<20 || state.Version != 1 || len(state.Drafts) > 64) {
		err = errors.New("unsupported or oversized UI state")
	}
	for _, d := range state.Drafts {
		if !utf8.ValidString(d.Text) || utf8.RuneCountInString(d.Text) > 16384 || d.Cursor < 0 || d.Cursor > utf8.RuneCountInString(d.Text) {
			err = errors.New("invalid saved UI draft")
		}
	}
	if err != nil {
		lock.Close()
		return nil, err
	}
	m.drafts = state.Drafts
	for name := range state.Drafts {
		if m.find(name) == nil {
			if isChannel(name) {
				m.ensureChannel(name)
			} else {
				m.buffers = append(m.buffers, newBuffer(name, bufQuery))
			}
		}
	}
	if m.find(state.Current) != nil {
		m.current = state.Current
		m.chosen = true
	}
	m.restoreDraft(m.current)
	s.last = data
	return s, nil
}

func (s *uiStateStore) save(m *uiModel) error {
	m.rememberDraft()
	if len(m.drafts) > 64 {
		return errors.New("UI draft limit reached (64); clear a draft before opening more")
	}
	state := uiSavedState{1, m.current, m.drafts}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if bytes.Equal(data, s.last) {
		return nil
	}
	if err := saveLocalState(s.path, state, atomicfile.SyncNone); err != nil {
		return err
	}
	s.last = data
	return nil
}

// Save the outbox intent and UI recovery handle before the backend can write.
func prepareUIDelivery(opt options, m *uiModel, cmds []uiCmd) error {
	for i := range cmds {
		cmd := &cmds[i]
		if cmd.kind != "send" && cmd.kind != "reply" {
			continue
		}
		cmd.text = irc.NormalizeMessage(cmd.text)
		if len(cmd.text) > 4096 {
			return errors.New("message exceeds 4096 bytes")
		}
		if !utf8.ValidString(cmd.text) || strings.TrimSpace(cmd.text) == "" {
			return errors.New("message must be nonempty UTF-8")
		}
		box, err := openOutbox(opt)
		if err != nil {
			return err
		}
		target, reply := cmd.target, ""
		if cmd.kind == "reply" {
			target, reply = "reply:"+cmd.target, cmd.target
		}
		entry, err := box.add(target, reply, cmd.text, "")
		box.close()
		if err != nil {
			return err
		}
		cmd.requestID = entry.RequestID
		m.uncertainID = entry.RequestID
		m.pending = cmd
	}
	return nil
}

func uncertainChat(cmd *uiCmd) bool {
	if cmd == nil || cmd.kind != "chat" || cmd.request == nil {
		return false
	}
	switch cmd.request.Action {
	case "action", "correct", "retract", "poll", "vote", "close-poll":
		return true
	}
	return false
}
