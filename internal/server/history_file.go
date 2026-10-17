package server

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// RestoreHistory opens the history file and its archive, loads the newest
// messages that fit the window, and appends every later message to it, so
// history survives a daemon restart and older cursors resume from the
// archive (see archive.go). It must be called before Serve.
func (s *Server) RestoreHistory(path string) error {
	if s.cfg.HistoryLimit == 0 {
		return errors.New("a history file requires a non-zero history limit")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.histFile != nil {
		return errors.New("history file is already open")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create history directory: %w", err)
	}
	a := newArchive(path, s.cfg.HistoryRetain, s.cfg.HistoryRetainBytes)
	if _, err := a.openSealed(); err != nil {
		return fmt.Errorf("open history archive: %w", err)
	}
	if err := dropSealedDuplicate(a, path); err != nil {
		return err
	}
	size, err := repairTail(path, s.cfg.Sync)
	if err != nil {
		return err
	}
	active := segment{path: path, size: size}
	all := a.segments(active)
	all = all[windowStart(all, s.cfg.HistoryLimit, s.cfg.HistoryBytes):]
	var activeFirst, last uint64
	var lastID string
	var newest time.Time
	records := 0
	s.histActive = map[string]activeRecord{}
	err = decodeSegments(all, func(i int, e *entry, next int64) {
		e.Persisted = true
		s.history.insert(e)
		a.noteCorrection(e.Message)
		last, lastID, newest = e.Seq, e.ID, e.Timestamp
		if i == len(all)-1 {
			if activeFirst == 0 {
				activeFirst = e.Seq
			}
			records++
			s.histActive[e.ID] = activeRecord{next, e.Seq}
		}
	})
	if err != nil {
		return fmt.Errorf("read history: %w", err)
	}
	s.seq = max(last, a.lastSealed())
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open history file: %w", err)
	}
	if err := errors.Join(atomicfile.File(file, s.cfg.Sync), syncHistoryDir(path, s.cfg.Sync)); err != nil {
		file.Close()
		return fmt.Errorf("sync restored history: %w", err)
	}
	s.histFile, s.histPath, s.archive = file, path, a
	s.commit.init(file, s.cfg.Sync)
	s.histBytes, s.histRecords, s.histFirstSeq = size, records, activeFirst
	s.histLastSeq, s.histLastID, s.histNewest = last, lastID, newest
	if s.histSegment <= 0 {
		s.histSegment = s.cfg.HistorySegmentBytes
	}
	if s.histSegment <= 0 {
		s.histSegment = defaultSegmentBytes
	}
	s.histStop = make(chan struct{})
	s.wg.Add(1)
	go s.historyJanitor(s.histStop)
	segments, archived := a.stats()
	s.logger.Info("history_restored", "path", path, "messages", s.history.size, "archive_segments", segments, "archive_bytes", archived)
	return nil
}

// activeRecord is where a message's line in the active file ends.
type activeRecord struct {
	next int64
	seq  uint64
}

// windowStart picks the oldest segment to load: the newest segments whose
// messages, by count and estimated memory, fill the window.
func windowStart(all []*segment, limit int, maxBytes int64) int {
	var count int
	var bytes int64
	start := len(all) - 1
	for ; start > 0; start-- {
		seg := all[start]
		n := seg.count
		if seg.records == 0 {
			n = int(seg.size / 300) // unindexed: assume short messages
		}
		count += n
		bytes += seg.size + int64(n)*entryOverhead
		if count >= limit || bytes >= maxBytes {
			break
		}
	}
	return start
}

// decodeSegments reads segments in parallel, a few ahead, and hands their
// messages to insert in order.
func decodeSegments(all []*segment, insert func(segment int, e *entry, next int64)) error {
	type batch struct {
		entries []*entry
		ends    []int64
		err     error
	}
	workers := max(1, min(runtime.GOMAXPROCS(0), 8))
	slots := make(chan struct{}, workers)
	results := make([]chan batch, len(all))
	for i := range all {
		results[i] = make(chan batch, 1)
	}
	go func() {
		for i, seg := range all {
			slots <- struct{}{} // released once segment i is inserted
			go func() {
				var b batch
				b.err = readSegment(seg.path, 0, seg.size, func(m Message, _, next int64) bool {
					b.entries = append(b.entries, newEntry(m))
					b.ends = append(b.ends, next)
					return true
				})
				if errors.Is(b.err, os.ErrNotExist) {
					b.err = nil
				}
				results[i] <- b
			}()
		}
	}()
	var failure error
	for i := range all {
		b := <-results[i]
		if b.err != nil && failure == nil {
			failure = b.err
		}
		if failure == nil {
			for j, e := range b.entries {
				insert(i, e, b.ends[j])
			}
		}
		<-slots
	}
	return failure
}

// dropSealedDuplicate removes a sealed link left by a crash during rotation,
// before the active file was replaced: it is the active file itself.
func dropSealedDuplicate(a *archive, path string) error {
	active, err := os.Stat(path)
	if err != nil {
		return nil
	}
	for i, seg := range a.sealed {
		if info, err := os.Stat(seg.path); err == nil && os.SameFile(active, info) {
			if err := os.Remove(seg.path); err != nil {
				return fmt.Errorf("remove interrupted rotation: %w", err)
			}
			_ = os.Remove(seg.path + ".idx")
			a.sealed = slices.Delete(a.sealed, i, i+1)
			return nil
		}
	}
	return nil
}

// repairTail cuts a crash-torn final line from the history file, so the
// next append starts on a line of its own, and returns the file's size.
func repairTail(path string, mode atomicfile.Sync) (int64, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open history file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	end := size
	var buf [4096]byte
	for end > 0 {
		chunk := min(end, int64(len(buf)))
		if _, err := file.ReadAt(buf[:chunk], end-chunk); err != nil {
			return 0, fmt.Errorf("read history file: %w", err)
		}
		if i := lastNewline(buf[:chunk]); i >= 0 {
			end = end - chunk + int64(i) + 1
			break
		}
		end -= chunk
	}
	if end == size {
		return size, nil
	}
	if err := file.Truncate(end); err != nil {
		return 0, fmt.Errorf("repair history file: %w", err)
	}
	if err := atomicfile.File(file, mode); err != nil {
		return 0, fmt.Errorf("repair history file: %w", err)
	}
	return end, nil
}

func lastNewline(b []byte) int {
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] == '\n' {
			return i
		}
	}
	return -1
}

// Later appends go to the replacement inode, so with full durability its
// directory entry must reach the disk too.
func syncHistoryDir(path string, mode atomicfile.Sync) error {
	if mode != atomicfile.SyncFull {
		return nil
	}
	if err := atomicfile.Dir(filepath.Dir(path)); err != nil {
		return fmt.Errorf("sync history directory: %w", err)
	}
	return nil
}

// rotateHistoryLocked seals the active file as an archive segment and starts
// a new one. It holds messageMu, so nothing is appended meanwhile, and syncs
// what earlier posts wrote first. The sealed name is a hard link, made before
// a new file replaces the active path, so the path never goes missing: a
// crash in between leaves a duplicate link that restore removes.
func (s *Server) rotateHistoryLocked() error {
	s.mu.Unlock()
	err := s.commit.drain()
	s.mu.Lock()
	if err != nil {
		return err
	}
	if s.histFirstSeq == 0 {
		return nil
	}
	path, old := s.histPath, s.histFile
	sealed := &segment{path: segmentPath(path, s.histFirstSeq), first: s.histFirstSeq, last: s.histLastSeq, lastID: s.histLastID, newest: s.histNewest, size: s.histBytes, count: s.histRecords}
	s.mu.Unlock()
	next, err := replaceHistoryFile(path, sealed.path, s.cfg.Sync)
	s.mu.Lock()
	if next == nil {
		return err
	}
	s.histFile = next
	s.commit.retire(old)
	s.commit.init(next, s.cfg.Sync)
	s.histBytes, s.histRecords, s.histFirstSeq = 0, 0, 0
	s.histActive = map[string]activeRecord{}
	s.archive.seal(sealed)
	return err // a failed directory sync, reported once the swap is done
}

// replaceHistoryFile seals path under the name sealed and opens a new, empty
// file at path. With no file returned nothing changed; an error beside a
// file means the swap happened but its directory entries were not synced.
func replaceHistoryFile(path, sealed string, mode atomicfile.Sync) (*os.File, error) {
	if err := os.Link(path, sealed); err != nil {
		return nil, fmt.Errorf("seal history segment: %w", err)
	}
	undo := func(err error) (*os.File, error) {
		_ = os.Remove(sealed)
		return nil, fmt.Errorf("start history segment: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".airc-history-*")
	if err != nil {
		return undo(err)
	}
	if err := errors.Join(temp.Chmod(0o600), temp.Close()); err != nil {
		_ = os.Remove(temp.Name())
		return undo(err)
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		_ = os.Remove(temp.Name())
		return undo(err)
	}
	next, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		// Put the full file back at path, over the empty one. If that fails
		// too, the sealed name keeps what is still appended to it, and a
		// restart reads it as a segment.
		_ = os.Rename(sealed, path)
		return nil, fmt.Errorf("open history segment: %w", err)
	}
	return next, syncHistoryDir(path, mode)
}

// historyJanitor indexes sealed segments and applies retention: once at
// start, after each rotation and hourly.
func (s *Server) historyJanitor(stop chan struct{}) {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		for _, seg := range s.archive.unindexed() {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.archive.index(seg); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.logger.Error("history_index_failed", "segment", seg.path, "error", err.Error())
			}
		}
		s.expireHistory()
		select {
		case <-stop:
			return
		case <-ticker.C:
		case <-s.archive.built:
		}
	}
}

// expireHistory applies the retention limits to the archive and the window.
func (s *Server) expireHistory() {
	now := s.now()
	s.mu.Lock()
	active := s.histBytes
	if s.cfg.HistoryRetain > 0 {
		s.history.dropBefore(now.Add(-s.cfg.HistoryRetain))
	}
	s.mu.Unlock()
	removed, err := s.archive.enforce(now, active)
	if err != nil {
		s.logger.Error("history_retention_failed", "error", err.Error())
	}
	if removed > 0 {
		s.logger.Info("history_segments_expired", "segments", removed)
	}
}

// activeSegmentLocked describes the active file for an archive read.
func (s *Server) activeSegmentLocked() segment {
	return segment{path: s.histPath, first: s.histFirstSeq, size: s.histBytes}
}
