//go:build darwin || linux

package adapter

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"os"

	"github.com/Someblueman/airc/gateway/internal/jsoninput"
)

const maxAppendClaimBytes = 32 << 10

type appendLogCheckpoint struct {
	Version int             `json:"version"`
	Cursor  AppendLogCursor `json:"cursor"`
}

type appendLogClaim struct {
	Version int             `json:"version"`
	Before  AppendLogCursor `json:"before"`
	Next    AppendLogCursor `json:"next"`
	Body    string          `json:"body"`
}

// AppendLogMailbox retains one exact record before returning it to its caller.
// Calls are sequential; publication advances the cursor after a synced result.
// Authentication and journal intake are separate from this filesystem boundary.
type AppendLogMailbox struct{ reader *AppendLogReader }

func OpenAppendLogMailbox(path string) (*AppendLogMailbox, error) {
	r, err := OpenAppendLogReader(path)
	if err != nil {
		return nil, err
	}
	return &AppendLogMailbox{r}, nil
}

func (m *AppendLogMailbox) Close() error { return m.reader.Close() }

func (m *AppendLogMailbox) cursor() (AppendLogCursor, error) {
	data, err := m.readState("cursor.json", 1024)
	if errors.Is(err, os.ErrNotExist) {
		return AppendLogCursor{Generation: 1}, nil
	}
	if err != nil {
		return AppendLogCursor{}, err
	}
	defer clear(data)
	var checkpoint appendLogCheckpoint
	if jsoninput.Decode(data, &checkpoint, 1024) != nil || checkpoint.Version != 1 || !validAppendLogCursor(checkpoint.Cursor) {
		return AppendLogCursor{}, errors.New("invalid append log checkpoint")
	}
	return checkpoint.Cursor, nil
}

func (c appendLogClaim) valid() bool {
	before, next := c.Before, c.Next
	body, err := base64.StdEncoding.DecodeString(c.Body)
	defer clear(body)
	if err != nil || base64.StdEncoding.EncodeToString(body) != c.Body || c.Version != 1 || !validAppendLogCursor(before) || !validAppendLogCursor(next) || len(body) > MaxRequestFrameBytes || bytes.ContainsRune(body, '\n') || next.Sealed != (len(body) == 0) {
		return false
	}
	if before.Sealed {
		return before.Generation < math.MaxUint64 && next.Generation == before.Generation+1 && next.Offset == int64(len(body))+1
	}
	return next.Generation == before.Generation && next.Offset == before.Offset+int64(len(body))+1 && (before.Inode == 0 || before.Inode == next.Inode && before.Device == next.Device)
}

func (c appendLogClaim) record() AppendLogRecord {
	body, _ := base64.StdEncoding.DecodeString(c.Body)
	return AppendLogRecord{Body: body, Next: c.Next}
}

func (m *AppendLogMailbox) claim() (appendLogClaim, error) {
	data, err := m.readState("claim.json", maxAppendClaimBytes)
	if err != nil {
		return appendLogClaim{}, err
	}
	defer clear(data)
	var claim appendLogClaim
	if jsoninput.Decode(data, &claim, maxAppendClaimBytes) != nil || !claim.valid() {
		return appendLogClaim{}, errors.New("invalid append log claim")
	}
	return claim, nil
}

func (m *AppendLogMailbox) removeClaim() error {
	// Retry a possibly interrupted cursor rename's directory sync before cleanup.
	if err := m.reader.files.syncDirectory("."); err != nil {
		return err
	}
	if err := m.reader.files.root.Remove("claim.json"); err != nil {
		return err
	}
	return m.reader.files.syncDirectory(".")
}

func (m *AppendLogMailbox) Claim() (AppendLogRecord, error) {
	if err := m.reader.private(); err != nil {
		return AppendLogRecord{}, err
	}
	if _, err := m.reader.files.root.Lstat("response.json"); err == nil {
		return AppendLogRecord{}, ErrMailboxResultPending
	} else if !errors.Is(err, os.ErrNotExist) {
		return AppendLogRecord{}, err
	}
	cursor, err := m.cursor()
	if err != nil {
		return AppendLogRecord{}, err
	}
	claim, err := m.claim()
	if err == nil {
		if cursor == claim.Before {
			if err := m.reader.files.syncDirectory("."); err != nil {
				return AppendLogRecord{}, err
			}
			return claim.record(), nil
		}
		if cursor != claim.Next {
			return AppendLogRecord{}, errors.New("append log claim and checkpoint disagree")
		}
		if err := m.removeClaim(); err != nil {
			return AppendLogRecord{}, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return AppendLogRecord{}, err
	}
	record, err := m.reader.Next(cursor)
	if err != nil {
		return AppendLogRecord{}, err
	}
	defer clear(record.Body)
	encoded, err := json.Marshal(appendLogClaim{Version: 1, Before: cursor, Next: record.Next, Body: base64.StdEncoding.EncodeToString(record.Body)})
	if err != nil {
		return AppendLogRecord{}, err
	}
	defer clear(encoded)
	if err := m.stage("claim.json", encoded); err != nil {
		return AppendLogRecord{}, err
	}
	// Dispatch the retained snapshot, including after a concurrent source change.
	retained, err := m.claim()
	if err != nil {
		return AppendLogRecord{}, err
	}
	return retained.record(), nil
}

// Publish syncs a complete response, then atomically checkpoints the consumed
// boundary and removes the claim. A seal requires no response and empty data.
func (m *AppendLogMailbox) Publish(data []byte) error {
	if err := m.reader.private(); err != nil {
		return err
	}
	claim, err := m.claim()
	if err != nil {
		return err
	}
	cursor, err := m.cursor()
	if err != nil {
		return err
	}
	if cursor != claim.Before {
		return errors.New("append log publication checkpoint mismatch")
	}
	if claim.Next.Sealed {
		if len(data) != 0 {
			return errors.New("append log seal cannot publish a response")
		}
		if _, err := m.reader.files.root.Lstat("response.json"); err == nil {
			return ErrMailboxResultPending
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	} else if err := m.stage("response.json", data); err != nil {
		return err
	}
	encoded, err := json.Marshal(appendLogCheckpoint{Version: 1, Cursor: claim.Next})
	if err != nil {
		return err
	}
	if err := m.stage("cursor.json", encoded); err != nil {
		return err
	}
	return m.removeClaim()
}
