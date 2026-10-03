package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// New durable chat stores use bounded reads and write-before-apply snapshots.
func readState(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 8<<20 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invalid or oversized chat state")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	if !onlyJSONEnd(d) {
		return errors.New("extra JSON in chat state")
	}
	return nil
}

func writeState(path string, value any, mode atomicfile.Sync) error {
	if path == "" {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600, mode)
}

// softSync is for state that is cheap to lose (headers, profiles): never a
// full device flush, since these writes happen under the server lock.
func (s *Server) softSync() atomicfile.Sync {
	return max(s.cfg.Sync, atomicfile.SyncFlush)
}
