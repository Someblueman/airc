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

// snapshot is how one JSON state file is read and replaced: a bounded read,
// one strict decode, and an atomic replace of the whole file. Stores that
// decode leniently or wrap their own errors read the bytes and decode them
// themselves.
type snapshot struct {
	limit    int64  // bytes the file may hold
	tooLarge string // the store's own wording for an oversized file
	invalid  string // when set, a bare "null" is refused with this wording
	indent   bool   // written with MarshalIndent (topics)
}

// chatSnapshot is the snapshot the durable chat stores (accounts, chat,
// wakes) use: bounded reads and write-before-apply.
var chatSnapshot = snapshot{limit: 8 << 20, tooLarge: "invalid or oversized chat state", invalid: "invalid or oversized chat state"}

// bytes reads the file at path, creating its directory first. found is
// false, with no error, when there is no file yet.
func (f snapshot) bytes(path string) (data []byte, found bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	data, err = atomicfile.ReadBounded(path, f.limit)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if errors.Is(err, atomicfile.ErrTooLarge) {
		return nil, false, errors.New(f.tooLarge)
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// read decodes the file at path strictly into value; a missing file leaves
// value as it is.
func (f snapshot) read(path string, value any) error {
	data, found, err := f.bytes(path)
	if err != nil || !found {
		return err
	}
	if f.invalid != "" && bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New(f.invalid)
	}
	return decodeStrict(bytes.NewReader(data), value)
}

// write replaces the file at path with value as one JSON document; "" is
// a store with no file.
func (f snapshot) write(path string, value any, mode atomicfile.Sync) error {
	if path == "" {
		return nil
	}
	var data []byte
	var err error
	if f.indent {
		data, err = json.MarshalIndent(value, "", "  ")
	} else {
		data, err = json.Marshal(value)
	}
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600, mode)
}

// New durable chat stores use bounded reads and write-before-apply snapshots.
func readState(path string, value any) error {
	return chatSnapshot.read(path, value)
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
	return chatSnapshot.write(path, value, mode)
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
