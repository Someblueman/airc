package server

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// appendLog is an append-only log of records, one JSON line each, shared by
// handoffs and decisions. Each new state of a record is appended; the newest
// line for a record wins. records counts the lines, superseded ones
// included. dirty marks a failed append, which may have left part of a
// line: the log is rewritten before anything more is appended.
type appendLog[T any] struct {
	path    string
	file    *os.File
	records int
	dirty   bool
}

// readLog loads the log at path: valid checks each record and returns its
// key. A torn last line, from a crash mid-write, is dropped; any other
// unreadable line is an error, so no record is silently lost.
func readLog[T any](path string, maxRecord int, valid func(T) (string, error)) (map[string]T, error) {
	loaded := map[string]T{}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return loaded, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, maxRecord)
	for n := 1; ; n++ {
		line, err := r.ReadSlice('\n')
		if errors.Is(err, io.EOF) {
			return loaded, nil // nothing more, or a torn last line
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, fmt.Errorf("line %d is too long", n)
		}
		if err != nil {
			return nil, err
		}
		var record T
		if err := decodeStrict(bytes.NewReader(line), &record); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		key, err := valid(record)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		loaded[key] = record
	}
}

// rewrite replaces the log with one line per record, in the order given,
// then appends to the new file.
func (l *appendLog[T]) rewrite(records []T, sync atomicfile.Sync) error {
	var data []byte
	for _, record := range records {
		line, err := json.Marshal(record)
		if err != nil {
			return err
		}
		data = append(append(data, line...), '\n')
	}
	if err := atomicfile.Write(l.path, data, 0o600, sync); err != nil {
		return err
	}
	file, err := os.OpenFile(l.path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	l.file, l.records, l.dirty = file, len(records), false
	return nil
}

// append writes one record's new state. A failed write marks the log dirty,
// since part of a line may be on disk.
func (l *appendLog[T]) append(record T, sync atomicfile.Sync) error {
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		l.dirty = true
		return err
	}
	if err := atomicfile.File(l.file, sync); err != nil {
		l.dirty = true
		return err
	}
	l.records++
	return nil
}

// crowded reports whether superseded lines outnumber the records held, so
// the log is worth compacting.
func (l *appendLog[T]) crowded(held int) bool {
	return l.records > 2*held+64
}

// close closes the log's file; every save completes under the server lock,
// so nothing is appending.
func (l *appendLog[T]) close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
