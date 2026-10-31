package server

import (
	"cmp"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

// A paste is text larger than a message, uploaded in chunks over the
// connection and kept by content hash: the same text uploaded twice is one
// paste. Each paste is one file under the pastes directory, named by its ID,
// and the index (pastes.json) holds what is known about it: name, language,
// size, author, when it was made, when it was last referenced and the
// messages that share it. A paste is kept for the retention period after
// its last reference (a message sharing it, or a read), and longer while a
// message sharing it is retained in history; the oldest unreferenced go
// first when the total size is exceeded.

const (
	defaultPasteBytes      = 64 << 10
	defaultPasteTotalBytes = 64 << 20
	defaultPasteRetain     = 7 * 24 * time.Hour
	maxPasteBytes          = 8 << 20
	maxPasteIndexBytes     = 8 << 20
	maxPasteCount          = 100000
	// maxPasteMessages bounds the sharing messages remembered per paste.
	maxPasteMessages = 32
	// pastePage is how many chunks one paste-get answers at most.
	pastePage = 16
	// pasteUploadTimeout discards an upload not finished in time.
	pasteUploadTimeout = 60 * time.Second
)

// pasteSnapshot reads and writes the index.
var pasteSnapshot = snapshot{limit: maxPasteIndexBytes, tooLarge: "invalid or oversized paste index", invalid: "invalid or oversized paste index"}

// pasteRecord is one paste as the index holds it.
type pasteRecord struct {
	ID         string    `json:"id"`
	Name       string    `json:"name,omitempty"`
	Language   string    `json:"language,omitempty"`
	Size       int       `json:"size"`
	Lines      int       `json:"lines"`
	Author     string    `json:"author"`
	AccountID  string    `json:"account_id,omitempty"`
	Created    time.Time `json:"created"`
	Referenced time.Time `json:"referenced"`
	// Messages are the IDs of messages that share the paste, oldest first.
	Messages []string `json:"messages,omitempty"`
}

func (r pasteRecord) info() protocol.PasteInfo {
	return protocol.PasteInfo{ID: r.ID, Name: r.Name, Language: r.Language, Size: r.Size, Lines: r.Lines}
}

type pasteIndex struct {
	Pastes []pasteRecord `json:"pastes"`
}

func validPasteRecord(r pasteRecord) error {
	if !protocol.ValidMessageID(r.ID) || r.Size < 1 || r.Size > maxPasteBytes || r.Lines < 0 || !protocol.ValidNick(r.Author) || r.Created.IsZero() || r.Referenced.IsZero() {
		return errors.New("invalid paste record")
	}
	if r.Name != "" && !protocol.ValidPasteName(r.Name) || !protocol.ValidPasteLanguage(r.Language) || len(r.Messages) > maxPasteMessages {
		return errors.New("invalid paste record")
	}
	for _, id := range r.Messages {
		if !protocol.ValidMessageID(id) {
			return errors.New("invalid paste record")
		}
	}
	return nil
}

// pasteStore is the pastes, their index and their files. Every field is
// read and written with s.mu held; the index is written before a change
// applies, with env.sync. With no index path the pastes, their content
// included, live in memory only.
type pasteStore struct {
	pastes   map[string]pasteRecord
	contents map[string][]byte // in memory only, without an index path
	indexAt  string
	dir      string
	total    int64
	env      *storeEnv
}

// pasteUpload is one upload in flight on a connection.
type pasteUpload struct {
	name, language string
	size, lines    int
	started        time.Time
	data           []byte
	next           int // the sequence number of the chunk expected next
}

// RestorePastes loads the index at path and the files in the directory
// named after it (DIR/pastes.json keeps its files in DIR/pastes/), and
// writes both from then on. An indexed paste whose file is missing or of
// another size is dropped from the index, with a warning; a file the index
// does not know is removed. Restore pastes after history, so the sweep that
// follows sees which sharing messages are retained.
func (s *Server) RestorePastes(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil || s.indexAt != "" {
		return errors.New("configure pastes once before Serve")
	}
	if err := s.pasteStore.load(path); err != nil {
		return err
	}
	s.sweepPastesLocked(s.now())
	return nil
}

// pasteDir is the directory the index at path keeps its files in.
func pasteDir(path string) string { return strings.TrimSuffix(path, ".json") }

func (st *pasteStore) load(path string) error {
	dir := pasteDir(path)
	if dir == path {
		return fmt.Errorf("pastes %s: the index is a .json file", path)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	var loaded pasteIndex
	if err := pasteSnapshot.read(path, &loaded); err != nil {
		return fmt.Errorf("pastes %s: %w", path, err)
	}
	if len(loaded.Pastes) > maxPasteCount {
		return fmt.Errorf("pastes %s: more than %d pastes", path, maxPasteCount)
	}
	pastes := map[string]pasteRecord{}
	var total int64
	dropped := false
	for _, r := range loaded.Pastes {
		if err := validPasteRecord(r); err != nil {
			return fmt.Errorf("pastes %s: %s: %w", path, r.ID, err)
		}
		if _, dup := pastes[r.ID]; dup {
			return fmt.Errorf("pastes %s: %s is indexed twice", path, r.ID)
		}
		info, err := os.Stat(filepath.Join(dir, r.ID))
		if err != nil || info.Size() != int64(r.Size) {
			st.env.logger.Warn("paste_file_missing", "id", r.ID, "path", filepath.Join(dir, r.ID))
			dropped = true
			continue
		}
		pastes[r.ID] = r
		total += int64(r.Size)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if _, indexed := pastes[e.Name()]; !indexed {
			st.env.logger.Warn("paste_file_unindexed", "path", filepath.Join(dir, e.Name()))
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	st.pastes, st.contents, st.indexAt, st.dir, st.total = pastes, nil, path, dir, total
	if dropped {
		return st.saveIndexLocked()
	}
	return nil
}

// close has nothing to close: every write completes under s.mu.
func (st *pasteStore) close() error { return nil }

// saveIndexLocked writes the index as it is now.
func (st *pasteStore) saveIndexLocked() error {
	if st.indexAt == "" {
		return nil
	}
	index := pasteIndex{Pastes: make([]pasteRecord, 0, len(st.pastes))}
	for _, r := range st.pastes {
		index.Pastes = append(index.Pastes, r)
	}
	slices.SortFunc(index.Pastes, func(a, b pasteRecord) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	return st.env.saved("pastes", pasteSnapshot.write(st.indexAt, index, st.env.sync))
}

// readPaste returns a paste's content.
func (st *pasteStore) readPaste(id string) ([]byte, error) {
	if st.indexAt == "" {
		data, ok := st.contents[id]
		if !ok {
			return nil, os.ErrNotExist
		}
		return data, nil
	}
	return atomicfile.ReadBounded(filepath.Join(st.dir, id), maxPasteBytes)
}

// removePasteLocked forgets a paste and deletes its file; the index is
// saved by the caller once its changes are complete.
func (st *pasteStore) removePasteLocked(id string) {
	r, ok := st.pastes[id]
	if !ok {
		return
	}
	delete(st.pastes, id)
	st.total -= int64(r.Size)
	if st.indexAt == "" {
		delete(st.contents, id)
		return
	}
	if err := os.Remove(filepath.Join(st.dir, id)); err != nil && !errors.Is(err, os.ErrNotExist) {
		st.env.logger.Warn("paste_remove_failed", "id", id, "error", err.Error())
	}
}

// pasteHeldLocked reports whether a message sharing r is still retained in
// history, and forgets the sharing messages that are not.
func (s *Server) pasteHeldLocked(r *pasteRecord) bool {
	kept := r.Messages[:0]
	for _, id := range r.Messages {
		if _, found := s.history.message(id); found {
			kept = append(kept, id)
		}
	}
	r.Messages = kept
	return len(kept) > 0
}

// sweepPastesLocked drops the pastes past their retention that no retained
// message shares, and saves the index when anything changed.
func (s *Server) sweepPastesLocked(now time.Time) {
	if s.cfg.PasteRetain <= 0 {
		return
	}
	changed := false
	for id, r := range s.pastes {
		if now.Sub(r.Referenced) <= s.cfg.PasteRetain {
			continue
		}
		if s.pasteHeldLocked(&r) {
			s.pastes[id] = r
			continue
		}
		s.removePasteLocked(id)
		changed = true
	}
	if changed {
		if err := s.saveIndexLocked(); err != nil {
			s.logger.Warn("paste_index_save_failed", "error", err.Error())
		}
	}
}

// makePasteRoomLocked evicts the oldest unreferenced pastes until need more
// bytes fit under the total limit, or reports that nothing more can go.
func (s *Server) makePasteRoomLocked(need int64) error {
	for s.total+need > s.cfg.PasteTotalBytes {
		victim := ""
		var oldest time.Time
		for id, r := range s.pastes {
			if s.pasteHeldLocked(&r) {
				s.pastes[id] = r
				continue
			}
			if victim == "" || r.Referenced.Before(oldest) {
				victim, oldest = id, r.Referenced
			}
		}
		if victim == "" {
			return errors.New("the paste store is full of pastes that retained messages share")
		}
		s.removePasteLocked(victim)
	}
	return nil
}

// pasteRequest is one paste action's view of the request.
type pasteRequest struct {
	Action   string
	ID       string
	Name     string
	Language string
	Size     int
	Lines    int
	Token    string
	Seq      int
	Data     []byte
	Offset   int
	Limit    int
}

func decodePasteRequest(cfg Config, r protocol.ChatRequest) (pasteRequest, error) {
	request := pasteRequest{Action: r.Action, ID: r.ID, Name: r.Name, Language: r.Language, Size: r.Size, Lines: r.Lines, Token: r.Token, Seq: r.Seq, Offset: r.Offset, Limit: r.Limit}
	switch r.Action {
	case "paste-begin":
		if !protocol.ValidRequestID(r.Token) {
			return request, errors.New("an upload token is 1-64 letters, digits, - or _")
		}
		if !protocol.ValidPasteName(r.Name) {
			return request, fmt.Errorf("a paste's name is 1-%d bytes of printable text without path separators", protocol.MaxPasteNameBytes)
		}
		if !protocol.ValidPasteLanguage(r.Language) {
			return request, errors.New("a paste's language is a short name such as go, python or text")
		}
		if r.Size < 1 || r.Size > cfg.PasteBytes {
			return request, fmt.Errorf("a paste is 1 to %d bytes", cfg.PasteBytes)
		}
		if r.Lines < 0 {
			return request, errors.New("a paste's line count is not negative")
		}
	case "paste-chunk", "paste-end":
		if !protocol.ValidRequestID(r.Token) {
			return request, errors.New("name the upload by its token")
		}
		if r.Action == "paste-chunk" {
			data, err := base64.StdEncoding.DecodeString(r.Data)
			if err != nil || len(data) < 1 || len(data) > protocol.PasteChunkBytes {
				return request, fmt.Errorf("a chunk is standard base64 of 1 to %d bytes", protocol.PasteChunkBytes)
			}
			if r.Seq < 0 {
				return request, errors.New("a chunk's sequence number counts from 0")
			}
			request.Data = data
		}
	case "paste-get":
		if !protocol.ValidMessageID(r.ID) {
			return request, errors.New("paste-get needs the paste's ID")
		}
		if r.Offset < 0 || r.Limit < 0 || r.Limit > pastePage {
			return request, fmt.Errorf("offset is not negative and limit is at most %d chunks", pastePage)
		}
	case "pastes":
		if r.ID != "" && !protocol.ValidMessageID(r.ID) {
			return request, errors.New("list one paste by its ID")
		}
		if r.Limit < 0 || r.Limit > maxHandoffPage {
			return request, fmt.Errorf("a paste listing holds at most %d", maxHandoffPage)
		}
	}
	return request, nil
}

// expireUploadsLocked discards client's uploads not finished in time.
func (s *Server) expireUploadsLocked(client *session, now time.Time) {
	for token, u := range client.uploads {
		if now.Sub(u.started) > pasteUploadTimeout {
			delete(client.uploads, token)
		}
	}
}

// uploadPasteLocked is the table's handler for paste-begin, paste-chunk and
// paste-end.
func (s *Server) uploadPasteLocked(client *session, r pasteRequest) error {
	now := s.now()
	s.expireUploadsLocked(client, now)
	switch r.Action {
	case "paste-begin":
		if _, exists := client.uploads[r.Token]; exists {
			return errors.New("an upload with this token is in flight; finish it or choose another token")
		}
		if len(client.uploads) >= protocol.MaxPasteUploads {
			return fmt.Errorf("%d uploads are in flight on this connection; finish one first", protocol.MaxPasteUploads)
		}
		if client.uploads == nil {
			client.uploads = map[string]*pasteUpload{}
		}
		client.uploads[r.Token] = &pasteUpload{name: r.Name, language: r.Language, size: r.Size, lines: r.Lines, started: now, data: make([]byte, 0, r.Size)}
		s.chatEntryLocked(client, protocol.ChatEntry{Action: r.Action, Token: r.Token})
		return nil
	}
	u, ok := client.uploads[r.Token]
	if !ok {
		return errors.New("unknown upload token: the upload was never begun, timed out or already ended")
	}
	switch r.Action {
	case "paste-chunk":
		if r.Seq != u.next {
			return fmt.Errorf("chunk %d expected, not %d", u.next, r.Seq)
		}
		if len(u.data)+len(r.Data) > u.size {
			delete(client.uploads, r.Token)
			return fmt.Errorf("the upload exceeds its declared %d bytes; begin again", u.size)
		}
		u.data = append(u.data, r.Data...)
		u.next++
		s.chatEntryLocked(client, protocol.ChatEntry{Action: r.Action, Token: r.Token, Offset: len(u.data)})
		return nil
	case "paste-end":
		delete(client.uploads, r.Token)
		if len(u.data) != u.size {
			return fmt.Errorf("the upload holds %d of its declared %d bytes", len(u.data), u.size)
		}
		if !protocol.ValidPasteText(u.data) {
			return errors.New("a paste is UTF-8 text without NUL bytes; binary is refused")
		}
		record, err := s.storePasteLocked(client, u, now)
		if err != nil {
			return err
		}
		s.chatEntryLocked(client, pasteEntry(record))
		return nil
	}
	return errors.New("unknown CHAT action")
}

// storePasteLocked keeps u's content as a paste, or refreshes the paste that
// already holds it, and returns its record.
func (s *Server) storePasteLocked(client *session, u *pasteUpload, now time.Time) (pasteRecord, error) {
	id := protocol.PasteID(u.data)
	if existing, ok := s.pastes[id]; ok {
		before := existing
		existing.Referenced = now.UTC()
		s.pastes[id] = existing
		if err := s.saveIndexLocked(); err != nil {
			s.pastes[id] = before
			return pasteRecord{}, err
		}
		return existing, nil
	}
	s.sweepPastesLocked(now)
	if err := s.makePasteRoomLocked(int64(u.size)); err != nil {
		return pasteRecord{}, err
	}
	record := pasteRecord{ID: id, Name: u.name, Language: u.language, Size: u.size, Lines: protocol.PasteLines(u.data), Author: client.client.Nick, AccountID: client.accountID, Created: now.UTC(), Referenced: now.UTC()}
	if s.indexAt == "" {
		if s.contents == nil {
			s.contents = map[string][]byte{}
		}
		s.contents[id] = u.data
	} else if err := atomicfile.Write(filepath.Join(s.dir, id), u.data, 0o600, s.env.sync); err != nil {
		return pasteRecord{}, err
	}
	s.pastes[id] = record
	s.total += int64(u.size)
	if err := s.saveIndexLocked(); err != nil {
		s.removePasteLocked(id)
		return pasteRecord{}, err
	}
	return record, nil
}

func pasteEntry(r pasteRecord) protocol.ChatEntry {
	info := r.info()
	return protocol.ChatEntry{Action: "paste", ID: r.ID, From: r.Author, AccountID: r.AccountID, Paste: &info, Created: r.Created, Referenced: r.Referenced}
}

// getPasteLocked answers a paste's content from an offset in entries of at
// most PasteChunkBytes each, the first of every page carrying the paste's
// metadata. A read counts as a reference.
func (s *Server) getPasteLocked(client *session, r pasteRequest) error {
	record, ok := s.pastes[r.ID]
	if !ok {
		return errors.New("paste is not retained")
	}
	if r.Offset > record.Size {
		return fmt.Errorf("offset %d is past the paste's %d bytes", r.Offset, record.Size)
	}
	data, err := s.readPaste(r.ID)
	if err != nil || len(data) != record.Size {
		s.logger.Warn("paste_file_unreadable", "id", r.ID)
		s.removePasteLocked(r.ID)
		if saveErr := s.saveIndexLocked(); saveErr != nil {
			s.logger.Warn("paste_index_save_failed", "error", saveErr.Error())
		}
		return errors.New("paste file is missing; the paste was dropped")
	}
	record.Referenced = s.now().UTC()
	s.pastes[r.ID] = record
	if err := s.saveIndexLocked(); err != nil {
		s.logger.Warn("paste_index_save_failed", "error", err.Error())
	}
	limit := cmp.Or(r.Limit, pastePage)
	first := pasteEntry(record)
	first.Action = "paste-get"
	first.Offset = r.Offset
	if r.Offset == record.Size {
		s.chatEntryLocked(client, first)
		return nil
	}
	for i, offset := 0, r.Offset; i < limit && offset < record.Size; i, offset = i+1, offset+protocol.PasteChunkBytes {
		end := min(offset+protocol.PasteChunkBytes, record.Size)
		entry := protocol.ChatEntry{Action: "paste-get", ID: r.ID, Offset: offset, Data: base64.StdEncoding.EncodeToString(data[offset:end])}
		if i == 0 {
			entry = first
			entry.Data = base64.StdEncoding.EncodeToString(data[offset:end])
		}
		s.chatEntryLocked(client, entry)
	}
	return nil
}

// listPastesLocked answers one paste by ID, or the caller's own pastes,
// oldest first, the newest limit of them.
func (s *Server) listPastesLocked(client *session, r pasteRequest) error {
	if r.ID != "" {
		record, ok := s.pastes[r.ID]
		if !ok {
			return errors.New("paste is not retained")
		}
		s.chatEntryLocked(client, pasteEntry(record))
		return nil
	}
	var out []pasteRecord
	for _, record := range s.pastes {
		mine := record.AccountID == client.accountID && (client.accountID != "" || strings.EqualFold(record.Author, client.client.Nick))
		if mine {
			out = append(out, record)
		}
	}
	slices.SortFunc(out, func(a, b pasteRecord) int {
		return cmp.Or(a.Created.Compare(b.Created), cmp.Compare(a.ID, b.ID))
	})
	out = out[max(0, len(out)-cmp.Or(r.Limit, handoffPage)):]
	for _, record := range out {
		s.chatEntryLocked(client, pasteEntry(record))
	}
	return nil
}

// referencePasteLocked records that m shares the paste its metadata names,
// so the paste outlives its retention while m is retained.
func (s *Server) referencePasteLocked(m Message) {
	if m.Paste == nil {
		return
	}
	record, ok := s.pastes[m.Paste.ID]
	if !ok {
		return
	}
	record.Referenced = m.Timestamp
	record.Messages = append(record.Messages, m.ID)
	if len(record.Messages) > maxPasteMessages {
		record.Messages = slices.Clone(record.Messages[len(record.Messages)-maxPasteMessages:])
	}
	s.pastes[m.Paste.ID] = record
	if err := s.saveIndexLocked(); err != nil {
		s.logger.Warn("paste_index_save_failed", "error", err.Error())
	}
}

// pasteInfoLocked describes the paste id names, for the message that
// shares it; nil when there is no such paste.
func (s *Server) pasteInfoLocked(id string) *protocol.PasteInfo {
	record, ok := s.pastes[id]
	if !ok {
		return nil
	}
	info := record.info()
	return &info
}
