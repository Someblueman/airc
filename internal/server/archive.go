package server

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

// The history archive keeps every message the window has dropped, up to the
// retention limits, so a cursor older than the window still resumes. The
// history file is the active segment; at segmentBytes it is sealed as
// <file>.<first seq> (20 digits) and a new one starts. Sealed segments never
// change. Each gets a sidecar index, <segment>.idx, of its message IDs and
// the rooms and inboxes it holds; a missing or damaged index is rebuilt.
// Retention deletes whole sealed segments, oldest first.

const (
	defaultSegmentBytes = 64 << 20
	indexMagic          = "AIRCIDX1"
)

// archiveBudget bounds the segment bytes one request reads; a read that runs
// out answers "more" with a cursor to continue from. Tests lower it.
var archiveBudget int64 = 32 << 20

var errArchiveGone = errors.New("the archived position is no longer retained")

// segment describes a sealed segment, or the active one.
type segment struct {
	path   string
	first  uint64 // sequence number of its first message
	last   uint64
	lastID string
	newest time.Time
	size   int64
	count  int
	// keys maps each window index key it holds to the offsets of the first
	// and last lines carrying it, so a read seeks past the rest; nil until
	// indexed.
	keys map[string][2]int64
	// records is where the sorted ID records start in the index; 0 until indexed.
	records     int64
	corrections []correction
}

// correction records that By supersedes ID.
type correction struct {
	ID, By    string
	Retracted bool
}

// location is a point in the archive: the segment, by its first sequence
// number, and the offset where the next line starts.
type location struct {
	first  uint64
	offset int64
}

type archive struct {
	mu          sync.Mutex
	base        string
	sealed      []*segment // ascending
	superseded  map[string]correction
	retain      time.Duration
	retainBytes int64
	built       chan struct{} // signalled when a segment is sealed
}

func newArchive(base string, retain time.Duration, retainBytes int64) *archive {
	return &archive{base: base, superseded: map[string]correction{}, retain: retain, retainBytes: retainBytes, built: make(chan struct{}, 1)}
}

func segmentPath(base string, first uint64) string { return fmt.Sprintf("%s.%020d", base, first) }

// openSealed lists the sealed segments next to base and reads their indexes.
// Segments without a valid index are listed for indexing.
func (a *archive) openSealed() (unindexed []*segment, err error) {
	matches, err := filepath.Glob(a.base + ".*")
	if err != nil {
		return nil, err
	}
	for _, path := range matches {
		suffix := strings.TrimPrefix(path, a.base+".")
		if len(suffix) != 20 || strings.Trim(suffix, "0123456789") != "" {
			continue
		}
		first, _ := strconv.ParseUint(suffix, 10, 64)
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		seg := &segment{path: path, first: first, size: info.Size()}
		if readIndex(seg) != nil {
			unindexed = append(unindexed, seg)
		}
		a.sealed = append(a.sealed, seg)
	}
	slices.SortFunc(a.sealed, func(x, y *segment) int { return compareSeq(x.first, y.first) })
	for _, seg := range a.sealed {
		for _, c := range seg.corrections {
			a.superseded[c.ID] = c
		}
	}
	return unindexed, nil
}

func compareSeq(x, y uint64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// archiveKeys are the index keys a target's messages carry, so a segment
// holding none of them can be skipped. Conversations are not indexed.
func archiveKeys(target string) []string {
	if _, _, ok := protocol.ConversationTarget(target); ok || target == "*" {
		return nil
	}
	if target == protocol.AllDirectMessages {
		return []string{"dm:*"}
	}
	if protocol.IsChannel(target) {
		return []string{target}
	}
	if nick, ok := strings.CutPrefix(target, "@"); ok {
		return []string{"dm:" + nickKey(nick), "mention:" + nickKey(nick)}
	}
	return []string{"dm:" + nickKey(target)}
}

// Index file: magic, a JSON header, then count records of a 16-byte ID hash
// and an 8-byte offset, sorted by hash. The header records the segment size,
// so an index of a different file is rejected.
type indexHeader struct {
	First       uint64              `json:"first"`
	Last        uint64              `json:"last"`
	LastID      string              `json:"last_id"`
	Newest      time.Time           `json:"newest"`
	Size        int64               `json:"size"`
	Count       int                 `json:"count"`
	Keys        map[string][2]int64 `json:"keys"`
	Corrections []correction        `json:"corrections,omitempty"`
}

const recordSize = 24

func idHash(id string) [16]byte {
	sum := sha256.Sum256([]byte(id))
	return [16]byte(sum[:16])
}

// readIndex loads seg's index header, or reports why it cannot be used.
func readIndex(seg *segment) error {
	file, err := os.Open(seg.path + ".idx")
	if err != nil {
		return err
	}
	defer file.Close()
	var prefix [len(indexMagic) + 4]byte
	if _, err := io.ReadFull(file, prefix[:]); err != nil || string(prefix[:len(indexMagic)]) != indexMagic {
		return errors.New("not a history index")
	}
	length := binary.LittleEndian.Uint32(prefix[len(indexMagic):])
	if length > 64<<20 {
		return errors.New("history index header is too large")
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(file, data); err != nil {
		return err
	}
	var h indexHeader
	if err := json.Unmarshal(data, &h); err != nil {
		return err
	}
	info, err := file.Stat()
	records := int64(len(prefix)) + int64(length)
	if err != nil || h.Size != seg.size || h.First != seg.first || info.Size() != records+int64(h.Count)*recordSize {
		return errors.New("history index does not match its segment")
	}
	seg.last, seg.lastID, seg.newest, seg.count, seg.corrections, seg.records = h.Last, h.LastID, h.Newest, h.Count, h.Corrections, records
	seg.keys = h.Keys
	if seg.keys == nil {
		seg.keys = map[string][2]int64{}
	}
	return nil
}

// writeIndex reads a sealed segment and writes its index.
func writeIndex(seg *segment) error {
	h := indexHeader{First: seg.first, Size: seg.size, Keys: map[string][2]int64{}}
	type record struct {
		hash   [16]byte
		offset int64
	}
	var records []record
	err := readSegment(seg.path, 0, seg.size, func(m Message, offset, _ int64) bool {
		records = append(records, record{idHash(m.ID), offset})
		h.Last, h.LastID, h.Count = m.Seq, m.ID, h.Count+1
		if m.Timestamp.After(h.Newest) {
			h.Newest = m.Timestamp
		}
		var mentions []string
		if protocol.IsChannel(m.Target) {
			mentions = protocol.Mentions(m.Body)
		}
		indexKeys(&m, mentions, func(key string) {
			if strings.HasPrefix(key, "thread:") || strings.HasPrefix(key, "child:") {
				return
			}
			span, seen := h.Keys[key]
			if !seen {
				span[0] = offset
			}
			span[1] = offset
			h.Keys[key] = span
		})
		if m.Supersedes != "" {
			h.Corrections = append(h.Corrections, correction{ID: m.Supersedes, By: m.ID, Retracted: m.Kind == "retract"})
		}
		return true
	})
	if err != nil {
		return err
	}
	slices.SortFunc(records, func(x, y record) int { return bytes.Compare(x.hash[:], y.hash[:]) })
	header, err := json.Marshal(h)
	if err != nil {
		return err
	}
	data := make([]byte, 0, len(indexMagic)+4+len(header)+len(records)*recordSize)
	data = append(data, indexMagic...)
	data = binary.LittleEndian.AppendUint32(data, uint32(len(header)))
	data = append(data, header...)
	for _, r := range records {
		data = append(data, r.hash[:]...)
		data = binary.LittleEndian.AppendUint64(data, uint64(r.offset))
	}
	// An index is rebuilt from its segment, so it needs no sync.
	if err := atomicfile.Write(seg.path+".idx", data, 0o600, atomicfile.SyncNone); err != nil {
		return err
	}
	return readIndex(seg)
}

// maxLineBytes bounds one history line; readSegment skips longer ones.
const maxLineBytes = 256 << 10

// readSegment visits the complete messages in path between offsets from and
// to, with each line's start and the offset after it. Unreadable lines are
// skipped, as they are on restore.
func readSegment(path string, from, to int64, visit func(m Message, start, next int64) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(io.NewSectionReader(file, from, to-from), maxLineBytes)
	offset := from
	for {
		line, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Longer than any message: skip to the end of the line.
			skipped := int64(len(line))
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = reader.ReadSlice('\n')
				skipped += int64(len(line))
			}
			offset += skipped
			continue
		}
		if err != nil {
			return nil // EOF, or a final line without its newline yet
		}
		start := offset
		offset += int64(len(line))
		var m Message
		if json.Unmarshal(line, &m) != nil || m.ID == "" || m.Target == "" {
			continue
		}
		if !visit(m, start, offset) {
			return nil
		}
	}
}

// segments returns the sealed segments and the active one, oldest first.
func (a *archive) segments(active segment) []*segment {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append(slices.Clone(a.sealed), &active)
}

// find locates the line after message id. It searches indexed segments
// newest first, then those it must scan, the active one last: a cursor
// older than the window is rarely in the active file.
func (a *archive) find(id string, active segment) (location, uint64, bool) {
	all := a.segments(active)
	slices.Reverse(all)
	scanned := func(s *segment) bool { return s.records == 0 }
	all = append(slices.DeleteFunc(slices.Clone(all), scanned), slices.DeleteFunc(all, func(s *segment) bool { return !scanned(s) })...)
	hash := idHash(id)
	for _, seg := range all {
		if seg.records == 0 {
			var found location
			var seq uint64
			_ = readSegment(seg.path, 0, seg.size, func(m Message, _, next int64) bool {
				if m.ID == id {
					found, seq = location{seg.first, next}, m.Seq
				}
				return found.offset == 0
			})
			if found.offset != 0 {
				return found, seq, true
			}
			continue
		}
		if loc, seq, ok := lookup(seg, id, hash); ok {
			return loc, seq, true
		}
	}
	return location{}, 0, false
}

// lookup binary-searches a sealed segment's index for id.
func lookup(seg *segment, id string, hash [16]byte) (location, uint64, bool) {
	file, err := os.Open(seg.path + ".idx")
	if err != nil {
		return location{}, 0, false
	}
	defer file.Close()
	var record [recordSize]byte
	at := func(i int) ([]byte, int64) {
		if _, err := file.ReadAt(record[:], seg.records+int64(i)*recordSize); err != nil {
			return nil, -1
		}
		return record[:16], int64(binary.LittleEndian.Uint64(record[16:]))
	}
	lo, hi := 0, seg.count
	for lo < hi {
		mid := (lo + hi) / 2
		key, _ := at(mid)
		if key == nil {
			return location{}, 0, false
		}
		if bytes.Compare(key, hash[:]) < 0 {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	for i := lo; i < seg.count; i++ {
		key, offset := at(i)
		if key == nil || !bytes.Equal(key, hash[:]) {
			break
		}
		var loc location
		var seq uint64
		_ = readSegment(seg.path, offset, seg.size, func(m Message, _, next int64) bool {
			if m.ID == id {
				loc, seq = location{seg.first, next}, m.Seq
			}
			return false
		})
		if loc.offset != 0 {
			return loc, seq, true
		}
	}
	return location{}, 0, false
}

// scanResult is where a scan stopped and why.
type scanResult struct {
	next      location
	lastID    string // the last message read, for a cursor to continue from
	lastSeq   uint64
	exhausted bool // the byte budget ran out
	stopped   bool // visit asked to stop
}

// scan reads archived messages after from, oldest first, through later
// segments, until a message at or past stop, visit returns false, or budget
// bytes have been read. A segment that holds none of keys is skipped.
func (a *archive) scan(from location, active segment, stop uint64, keys []string, budget int64, visit func(Message) bool) (scanResult, error) {
	all := a.segments(active)
	i := slices.IndexFunc(all, func(s *segment) bool { return s.first == from.first })
	if i < 0 {
		return scanResult{next: from}, errArchiveGone
	}
	result := scanResult{next: from}
	var read int64
	for ; i < len(all); i++ {
		seg := all[i]
		if seg.first >= stop && seg.first != from.first {
			return result, nil
		}
		offset, end := int64(0), seg.size
		if seg.first == from.first {
			offset = from.offset
		}
		if keys != nil && seg.keys != nil {
			// Read only the span of lines that can hold the target.
			first, last, found := seg.size, int64(-1), false
			for _, key := range keys {
				if span, ok := seg.keys[key]; ok {
					first, last, found = min(first, span[0]), max(last, span[1]), true
				}
			}
			if !found || last < offset {
				if seg.last >= stop {
					result.lastSeq = max(result.lastSeq, stop-1)
					return result, nil // nothing more before the window
				}
				result.next, result.lastID, result.lastSeq = location{seg.first, seg.size}, seg.lastID, seg.last
				continue
			}
			offset = max(offset, first)
			// The last span line runs to the next newline; read to it.
			end = min(seg.size, last+maxLineBytes)
		}
		done := false
		err := readSegment(seg.path, offset, end, func(m Message, _, next int64) bool {
			if m.Seq >= stop {
				done = true
				return false
			}
			read += next - offset
			offset = next
			result.next, result.lastID, result.lastSeq = location{seg.first, next}, m.ID, m.Seq
			a.annotate(&m)
			if !visit(m) {
				done, result.stopped = true, true
				return false
			}
			if read >= budget {
				done, result.exhausted = true, true
				return false
			}
			return true
		})
		if errors.Is(err, os.ErrNotExist) {
			continue // deleted by retention while being read
		}
		if err != nil || done {
			return result, err
		}
		if end < seg.size {
			// Past the target's last line: the rest of the segment is skipped.
			if seg.last >= stop {
				result.lastSeq = max(result.lastSeq, stop-1)
				return result, nil
			}
			result.next, result.lastID, result.lastSeq = location{seg.first, seg.size}, seg.lastID, seg.last
		}
	}
	return result, nil
}

func (a *archive) annotate(m *Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if c, ok := a.superseded[m.ID]; ok {
		m.SupersededBy, m.Retracted = c.By, c.Retracted
	}
	m.Persisted = true
}

func (a *archive) noteCorrection(m Message) {
	if m.Supersedes == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.superseded[m.Supersedes] = correction{ID: m.Supersedes, By: m.ID, Retracted: m.Kind == "retract"}
}

// seal adds a segment that was just sealed; the janitor indexes it.
func (a *archive) seal(seg *segment) {
	a.mu.Lock()
	a.sealed = append(a.sealed, seg)
	a.mu.Unlock()
	select {
	case a.built <- struct{}{}:
	default:
	}
}

// unindexed returns sealed segments still without an index.
func (a *archive) unindexed() []*segment {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []*segment
	for _, seg := range a.sealed {
		if seg.records == 0 {
			out = append(out, seg)
		}
	}
	return out
}

// index writes seg's index and publishes it.
func (a *archive) index(seg *segment) error {
	built := &segment{path: seg.path, first: seg.first, size: seg.size}
	if err := writeIndex(built); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if i := slices.Index(a.sealed, seg); i >= 0 {
		a.sealed[i] = built
	}
	for _, c := range built.corrections {
		a.superseded[c.ID] = c
	}
	return nil
}

// enforce deletes the oldest sealed segments beyond the retention limits.
// activeSize counts toward the size limit; the active segment is kept.
func (a *archive) enforce(now time.Time, activeSize int64) (removed int, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	total := activeSize
	for _, seg := range a.sealed {
		total += seg.size
	}
	for len(a.sealed) > 0 {
		seg := a.sealed[0]
		old := a.retain > 0 && seg.records != 0 && seg.newest.Before(now.Add(-a.retain))
		if !old && (a.retainBytes <= 0 || total <= a.retainBytes) {
			break
		}
		if err := os.Remove(seg.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
		_ = os.Remove(seg.path + ".idx")
		total -= seg.size
		for _, c := range seg.corrections {
			delete(a.superseded, c.ID)
		}
		a.sealed = a.sealed[1:]
		removed++
	}
	return removed, nil
}

// lastSealed returns the newest sealed segment's last sequence number.
func (a *archive) lastSealed() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.sealed) == 0 {
		return 0
	}
	seg := a.sealed[len(a.sealed)-1]
	if seg.last == 0 {
		// Unindexed: read its last message.
		_ = readSegment(seg.path, 0, seg.size, func(m Message, _, _ int64) bool {
			seg.last = m.Seq
			return true
		})
	}
	return seg.last
}

// stats reports the sealed segments' count and bytes and the oldest message time.
func (a *archive) stats() (segments int, size int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, seg := range a.sealed {
		size += seg.size
	}
	return len(a.sealed), size
}
