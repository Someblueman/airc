package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Someblueman/airc/internal/protocol"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

const maxOutboxEntries = 128

type outboundMessage struct {
	RequestID string      `json:"request_id"`
	Target    string      `json:"target"`
	ReplyTo   string      `json:"reply_to,omitempty"`
	Body      string      `json:"body"`
	Result    *sendResult `json:"result,omitempty"`
}

type outbox struct {
	path    string
	lock    *os.File
	Entries []outboundMessage `json:"entries"`
}

func openOutbox(opt options) (*outbox, error) {
	path, _, err := cursorPath(opt, opt.nick)
	if err != nil {
		return nil, err
	}
	path = filepath.Join(filepath.Dir(path), "outbox-"+filepath.Base(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another send for this nickname is updating its outbox: %w", err)
	}
	b := &outbox{path: path, lock: lock}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		b.close()
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 8<<20+1))
	if err == nil && len(data) > 8<<20 {
		err = errors.New("outbox exceeds 8 MiB")
	}
	if err == nil {
		err = json.Unmarshal(data, b)
	}
	if err == nil && len(b.Entries) > maxOutboxEntries {
		err = errors.New("outbox exceeds 128 entries")
	}
	if err == nil {
		seen := map[string]bool{}
		for _, e := range b.Entries {
			if !protocol.ValidRequestID(e.RequestID) || seen[e.RequestID] || len(e.Body) > 4096 || e.Result != nil && e.Result.MessageEvent == nil {
				err = errors.New("invalid outbox entry")
				break
			}
			seen[e.RequestID] = true
		}
	}
	if err != nil {
		b.close()
		return nil, fmt.Errorf("read outbox: %w", err)
	}
	return b, nil
}

func (b *outbox) close() {
	if b.lock != nil {
		b.lock.Close()
	}
}
func (b *outbox) find(id string) *outboundMessage {
	for i := range b.Entries {
		if b.Entries[i].RequestID == id {
			return &b.Entries[i]
		}
	}
	return nil
}

func (b *outbox) add(target, reply, body, id string) (*outboundMessage, error) {
	if id == "" {
		var raw [16]byte
		if _, err := rand.Read(raw[:]); err != nil {
			return nil, err
		}
		id = hex.EncodeToString(raw[:])
	}
	if old := b.find(id); old != nil {
		if old.Target != target || old.ReplyTo != reply || old.Body != body {
			return nil, &commandFailure{Type: "error", Code: "request_conflict", Phase: "outbox", Message: "request ID already has different content in the outbox"}
		}
		return old, nil
	}
	if len(b.Entries) == maxOutboxEntries {
		index := -1
		for i, e := range b.Entries {
			if e.Result != nil {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, errors.New("outbox has 128 uncertain sends; recover them with send --retry before sending more")
		}
		b.Entries = append(b.Entries[:index], b.Entries[index+1:]...)
	}
	b.Entries = append(b.Entries, outboundMessage{RequestID: id, Target: target, ReplyTo: reply, Body: body})
	if err := b.save(); err != nil {
		return nil, err
	}
	return &b.Entries[len(b.Entries)-1], nil
}

// Persist the intent before any wire write, and confirmed receipt before output.
func (b *outbox) save() error { return saveLocalState(b.path, b) }

func saveLocalState(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".outbox-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err == nil {
		dir, openErr := os.Open(filepath.Dir(path))
		if openErr != nil {
			return openErr
		}
		err = dir.Sync()
		dir.Close()
	}
	return err
}
