//go:build darwin || linux

package adapter

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
)

const MaxAppendLogBytes = 8 << 20

var ErrAppendLogIncomplete = errors.New("append log has an incomplete bounded record")

// AppendLogCursor describes a consumed boundary. Next proposes a new cursor;
// it does not persist it or establish authenticated intake.
type AppendLogCursor struct {
	Generation uint64 `json:"generation"`
	Offset     int64  `json:"offset"`
	Device     uint64 `json:"device"`
	Inode      uint64 `json:"inode"`
	Sealed     bool   `json:"sealed"`
}

type AppendLogRecord struct {
	Body []byte
	Next AppendLogCursor
}

// AppendLogReader exclusively owns a private root with an existing private logs
// subdirectory. Producers append bounded newline records and seal with an empty
// line before rotating. Calls are sequential; cursor persistence belongs to the caller.
type AppendLogReader struct{ files *FileMailbox }

func OpenAppendLogReader(path string) (*AppendLogReader, error) {
	files, err := OpenFileMailbox(path)
	if err != nil {
		return nil, err
	}
	r := &AppendLogReader{files}
	if err := r.private(); err != nil {
		_ = files.Close()
		return nil, err
	}
	return r, nil
}

func (r *AppendLogReader) Close() error { return r.files.Close() }

func (r *AppendLogReader) private() error {
	if err := r.files.private(); err != nil {
		return err
	}
	info, err := r.files.root.Lstat("logs")
	if err != nil {
		return fmt.Errorf("append log directory unavailable: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 || !unixPathOwner(info) {
		return errors.New("append log requires a private owned logs directory")
	}
	return nil
}

func appendLogName(generation uint64) string { return fmt.Sprintf("%020d.jsonl", generation) }

func (r *AppendLogReader) generations() ([]uint64, error) {
	f, err := r.files.root.Open("logs")
	if err != nil {
		return nil, err
	}
	entries, readErr := f.ReadDir(MaxDirectoryMailboxFiles + 1)
	closeErr := f.Close()
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(entries) > MaxDirectoryMailboxFiles {
		return nil, errors.New("append log generation quota exceeded")
	}
	generations := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		value, err := strconv.ParseUint(strings.TrimSuffix(entry.Name(), ".jsonl"), 10, 64)
		if err != nil || value == 0 || entry.Name() != appendLogName(value) {
			return nil, errors.New("append log requires canonical positive generation names")
		}
		generations = append(generations, value)
	}
	return generations, nil
}

// Next reads at most one bounded record. An empty line seals the generation and
// must be its final byte. EOF or an incomplete record never advances the cursor.
func (r *AppendLogReader) Next(cursor AppendLogCursor) (AppendLogRecord, error) {
	if err := r.private(); err != nil {
		return AppendLogRecord{}, err
	}
	if cursor.Generation == 0 || cursor.Offset < 0 || cursor.Offset > MaxAppendLogBytes || cursor.Offset > 0 && cursor.Inode == 0 || cursor.Offset == 0 && (cursor.Inode != 0 || cursor.Device != 0 || cursor.Sealed) {
		return AppendLogRecord{}, errors.New("invalid append log cursor")
	}
	generations, err := r.generations()
	if err != nil {
		return AppendLogRecord{}, err
	}
	if cursor.Sealed {
		f, info, err := r.open(cursor.Generation)
		if err == nil {
			_ = f.Close()
			if info.Device != cursor.Device || info.Inode != cursor.Inode || info.Size != cursor.Offset {
				return AppendLogRecord{}, errors.New("sealed append log changed")
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return AppendLogRecord{}, err
		}
		if cursor.Generation == math.MaxUint64 {
			return AppendLogRecord{}, errors.New("append log generation exhausted")
		}
		cursor = AppendLogCursor{Generation: cursor.Generation + 1}
	}
	f, info, err := r.open(cursor.Generation)
	if errors.Is(err, os.ErrNotExist) {
		if cursor.Offset != 0 {
			return AppendLogRecord{}, errors.New("unsealed append log disappeared")
		}
		for _, generation := range generations {
			if generation > cursor.Generation {
				return AppendLogRecord{}, errors.New("append log generation gap")
			}
		}
		return AppendLogRecord{}, io.EOF
	}
	if err != nil {
		return AppendLogRecord{}, err
	}
	defer f.Close()
	if cursor.Inode != 0 && (cursor.Device != info.Device || cursor.Inode != info.Inode) || info.Size < cursor.Offset {
		return AppendLogRecord{}, errors.New("append log replaced or truncated")
	}
	buffer := make([]byte, MaxRequestFrameBytes+2)
	n, err := f.ReadAt(buffer, cursor.Offset)
	if err != nil && !errors.Is(err, io.EOF) {
		return AppendLogRecord{}, err
	}
	buffer = buffer[:n]
	end := bytes.IndexByte(buffer, '\n')
	if end < 0 {
		if len(buffer) > MaxRequestFrameBytes {
			return AppendLogRecord{}, errors.New("append log record exceeds frame bound")
		}
		if n == 0 {
			return AppendLogRecord{}, io.EOF
		}
		return AppendLogRecord{}, ErrAppendLogIncomplete
	}
	if end > MaxRequestFrameBytes {
		return AppendLogRecord{}, errors.New("append log record exceeds frame bound")
	}
	next := AppendLogCursor{Generation: cursor.Generation, Offset: cursor.Offset + int64(end) + 1, Device: info.Device, Inode: info.Inode, Sealed: end == 0}
	if err := f.Sync(); err != nil {
		return AppendLogRecord{}, err
	}
	current, err := appendLogFileInfo(f)
	if err != nil {
		return AppendLogRecord{}, err
	}
	pathInfo, err := r.files.root.Lstat("logs/" + appendLogName(cursor.Generation))
	openedInfo, statErr := f.Stat()
	if err != nil || statErr != nil || !os.SameFile(pathInfo, openedInfo) || current.Size < next.Offset || next.Sealed && current.Size != next.Offset {
		return AppendLogRecord{}, errors.New("append log changed during read or has data after seal")
	}
	return AppendLogRecord{Body: buffer[:end], Next: next}, nil
}
