package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"github.com/Someblueman/airc/internal/atomicfile"
	"github.com/Someblueman/airc/internal/protocol"
)

const (
	maxOutboxEntries = 128
	maxOutboxBytes   = 8 << 20
)

type outboundMessage struct {
	RequestID string      `json:"request_id"`
	Target    string      `json:"target"`
	ReplyTo   string      `json:"reply_to,omitempty"`
	Body      string      `json:"body"`
	Result    *sendResult `json:"result,omitempty"`
	// Created is when the send was recorded, before its first wire write;
	// entries from older builds have none.
	Created time.Time `json:"created,omitzero"`
	// Paste is the paste the message shares; such a send is only recovered
	// over native IRC, never reposted through another carrier.
	Paste string `json:"paste,omitempty"`
	// Run is the run label the message carries, so a retry over another
	// carrier posts it with the label.
	Run string `json:"run,omitempty"`
}

// How long a send waits for another send on the same identity; a variable so
// tests can shorten it.
var outboxLockWait = 5 * time.Second

type outbox struct {
	path    string
	lock    *os.File
	Entries []outboundMessage `json:"entries"`
}

// outboxPath is where opt's nickname keeps its sends to opt's endpoint.
func outboxPath(opt options) (string, error) {
	path, _, err := cursorPath(opt, opt.nick)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(path), "outbox-"+filepath.Base(path)), nil
}

func openOutbox(opt options) (*outbox, error) {
	path, err := outboxPath(opt)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	// Sends hold the lock across their round trip; parallel sends queue here.
	if err := lockState(lock, outboxLockWait); err != nil {
		lock.Close()
		return nil, fmt.Errorf("another send for this nickname is updating its outbox; retry shortly: %w", err)
	}
	b := &outbox{path: path, lock: lock}
	data, err := atomicfile.ReadBounded(path, maxOutboxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if errors.Is(err, atomicfile.ErrTooLarge) {
		err = errors.New("outbox exceeds 8 MiB")
	} else if err != nil {
		b.close()
		return nil, err
	}
	if err == nil {
		b.Entries, err = decodeOutbox(data)
	}
	if err != nil {
		// A damaged outbox must not block every later send. Keep it for
		// inspection; its uncertain sends can no longer be recovered by ID.
		bad, moveErr := atomicfile.SetAside(path)
		if moveErr != nil {
			b.close()
			return nil, fmt.Errorf("read outbox %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "airc: warning: the send outbox was unreadable (%v) and was moved to %s; earlier uncertain sends cannot be recovered with --retry\n", err, bad)
		b.Entries = nil
	}
	return b, nil
}

// decodeOutbox parses an outbox file, checking every entry.
func decodeOutbox(data []byte) ([]outboundMessage, error) {
	var b outbox
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if len(b.Entries) > maxOutboxEntries {
		return nil, errors.New("outbox exceeds 128 entries")
	}
	seen := map[string]bool{}
	for _, e := range b.Entries {
		if !protocol.ValidRequestID(e.RequestID) || seen[e.RequestID] || len(e.Body) > 4096 || e.Result != nil && e.Result.MessageEvent == nil {
			return nil, errors.New("invalid outbox entry")
		}
		seen[e.RequestID] = true
	}
	return b.Entries, nil
}

// pendingOutbox lists the sends in opt's outbox whose outcome is unknown,
// what send --pending lists, without taking the outbox lock or moving a
// damaged outbox aside: the file is replaced atomically, so an unlocked read
// sees a whole one, and a reader changes nothing. A send in flight is pending
// until its receipt is saved. A missing outbox is empty.
func pendingOutbox(opt options) ([]outboundMessage, error) {
	path, err := outboxPath(opt)
	if err != nil {
		return nil, err
	}
	data, err := atomicfile.ReadBounded(path, maxOutboxBytes)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if errors.Is(err, atomicfile.ErrTooLarge) {
		return nil, errors.New("outbox exceeds 8 MiB")
	}
	if err != nil {
		return nil, err
	}
	entries, err := decodeOutbox(data)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(entries, func(e outboundMessage) bool { return e.Result != nil }), nil
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
		var err error
		if id, err = randomHex(16); err != nil {
			return nil, err
		}
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
	b.Entries = append(b.Entries, outboundMessage{RequestID: id, Target: target, ReplyTo: reply, Body: body, Created: time.Now().UTC()})
	if err := b.save(); err != nil {
		return nil, err
	}
	return &b.Entries[len(b.Entries)-1], nil
}

// remove forgets an entry locally; the caller saves.
func (b *outbox) remove(id string) {
	b.Entries = slices.DeleteFunc(b.Entries, func(e outboundMessage) bool { return e.RequestID == id })
}

// Persist the intent before any wire write, and confirmed receipt before output.
func (b *outbox) save() error { return saveLocalState(b.path, b, cliSync()) }

// cliSync is the durability of the send outbox. An fsync protects the intent
// against a crash of this process or the OS; AIRC_SYNC=full also flushes the
// drive cache, which costs several milliseconds per send on macOS.
func cliSync() atomicfile.Sync {
	if mode, err := atomicfile.ParseSync(os.Getenv("AIRC_SYNC")); err == nil {
		return mode
	}
	return atomicfile.SyncFlush
}

func saveLocalState(path string, value any, mode atomicfile.Sync) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicfile.Write(path, data, 0o600, mode)
}

// lockState takes an exclusive lock, waiting briefly for a concurrent command
// on the same identity to finish instead of failing at once.
func lockState(lock *os.File, wait time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil || !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
