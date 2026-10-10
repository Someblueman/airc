package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
)

// New durable chat stores use bounded reads and write-before-apply snapshots.
func readState(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := atomicfile.ReadBounded(path, 8<<20)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if errors.Is(err, atomicfile.ErrTooLarge) {
		return errors.New("invalid or oversized chat state")
	}
	if err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("invalid or oversized chat state")
	}
	return decodeStrict(bytes.NewReader(data), value)
}

// decodeStrict reads exactly one JSON value with no unknown fields and nothing after it.
func decodeStrict(r io.Reader, value any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil {
		return err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return errors.New("extra data after JSON value")
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

func (s *Server) now() time.Time {
	if s.cfg.Now != nil {
		return s.cfg.Now()
	}
	return time.Now()
}

// softSync is for state that is cheap to lose (headers, profiles): never a
// full device flush, since these writes happen under the server lock.
func (s *Server) softSync() atomicfile.Sync {
	return max(s.cfg.Sync, atomicfile.SyncFlush)
}
