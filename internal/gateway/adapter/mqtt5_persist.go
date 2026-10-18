package adapter

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Someblueman/airc/internal/gateway/mqttwire"
)

// mqtt5SnapshotBytes caps a sessions snapshot: the sessions the listener
// keeps, each with its unacknowledged messages.
const mqtt5SnapshotBytes = 512 << 20

// mqtt5Snapshot is a listener's sessions written to its state file, so
// they outlive a gateway restart. Wills are not kept: they hold their
// author's credential.
type mqtt5Snapshot struct {
	Version  int                    `json:"version"`
	Sessions []mqtt5SnapshotSession `json:"sessions"`
}

type mqtt5SnapshotSession struct {
	ClientID  string                  `json:"client_id"`
	Owner     string                  `json:"owner"`
	Expiry    time.Duration           `json:"expiry"`
	ExpiresAt time.Time               `json:"expires_at"`
	NextID    uint16                  `json:"next_id"`
	Subs      []mqtt5SnapshotSub      `json:"subscriptions"`
	Inflight  []mqtt5SnapshotOutbound `json:"inflight,omitempty"`
}

type mqtt5SnapshotSub struct {
	Filter string    `json:"filter"`
	Kind   mqtt5Kind `json:"kind"`
	Target string    `json:"target"`
	QoS    byte      `json:"qos"`
	After  string    `json:"after"`
}

type mqtt5SnapshotOutbound struct {
	ID    uint16              `json:"id"`
	Topic string              `json:"topic"`
	Props mqttwire.Properties `json:"props"`
	Body  []byte              `json:"body"`
}

// save writes the store's sessions to its file, replacing the last
// snapshot at once. A session a connection holds counts as released now,
// as it will be by the time the gateway restarts. A snapshot only lags:
// a session restored from it redelivers what was sent since, never skips.
func (s *mqtt5Store) save() error {
	if s.path == "" {
		return nil
	}
	now := time.Now()
	snapshot := mqtt5Snapshot{Version: 1}
	s.mu.Lock()
	s.sweepLocked(now)
	for id, state := range s.sessions {
		state.mu.Lock()
		saved := mqtt5SnapshotSession{ClientID: id, Owner: state.owner, Expiry: state.expiry, ExpiresAt: state.expiresAt, NextID: state.nextID}
		if saved.ExpiresAt.IsZero() {
			saved.ExpiresAt = now.Add(state.expiry)
		}
		for _, sub := range state.subs {
			saved.Subs = append(saved.Subs, mqtt5SnapshotSub{Filter: sub.filter, Kind: sub.kind, Target: sub.target, QoS: sub.qos, After: sub.after})
		}
		for _, out := range state.inflight {
			saved.Inflight = append(saved.Inflight, mqtt5SnapshotOutbound{ID: out.id, Topic: out.topic, Props: out.props, Body: out.body})
		}
		state.mu.Unlock()
		snapshot.Sessions = append(snapshot.Sessions, saved)
	}
	s.mu.Unlock()
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".mqtt-sessions-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), s.path)
}

// load restores the sessions in the store's file that have not expired.
// A missing file is an empty store; an unreadable one is an error, so a
// gateway does not quietly forget its clients' sessions.
func (s *mqtt5Store) load() error {
	if s.path == "" {
		return nil
	}
	link, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !link.Mode().IsRegular() {
		// Checked before opening too: opening a FIFO would wait for a
		// writer.
		return errors.New("MQTT sessions file must be a private regular file")
	}
	file, err := os.Open(s.path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(link, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > mqtt5SnapshotBytes {
		return errors.New("MQTT sessions file must be a private regular file")
	}
	var snapshot mqtt5Snapshot
	if err := json.NewDecoder(io.LimitReader(file, mqtt5SnapshotBytes)).Decode(&snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 {
		return errors.New("unknown MQTT sessions file version")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, saved := range snapshot.Sessions {
		if saved.ClientID == "" || s.sessions[saved.ClientID] != nil || saved.Owner == "" || !now.Before(saved.ExpiresAt) || saved.Expiry <= 0 || saved.Expiry > mqtt5MaxSessionExpiry || len(s.sessions) >= mqtt5MaxSessions || s.ownedLocked(saved.Owner) >= mqtt5MaxSessionsPerOwner {
			continue
		}
		if state := restoreMQTT5Session(saved); state != nil {
			s.sessions[saved.ClientID] = state
		}
	}
	return nil
}

// restoreMQTT5Session rebuilds a saved session within the bounds a live
// one keeps, or answers nil: a file is not trusted to be one this gateway
// wrote.
func restoreMQTT5Session(saved mqtt5SnapshotSession) *mqtt5State {
	if len(saved.Subs) > mqtt5MaxFollowTargets+mqtt5MaxReplyTopics || len(saved.Inflight) > mqtt5MaxInflight {
		return nil
	}
	state := &mqtt5State{owner: saved.Owner, expiry: saved.Expiry, expiresAt: saved.ExpiresAt, nextID: saved.NextID, subs: map[string]*mqtt5Sub{}}
	for _, sub := range saved.Subs {
		parsed, reason := (&mqtt5Conn{}).classify(sub.Filter)
		if reason != mqttSuccess || parsed.kind != sub.Kind || parsed.target != sub.Target || sub.QoS > 1 || state.subs[sub.Filter] != nil {
			return nil
		}
		state.subs[sub.Filter] = &mqtt5Sub{filter: sub.Filter, kind: sub.Kind, target: sub.Target, qos: sub.QoS, after: sub.After}
	}
	ids := map[uint16]bool{}
	for _, out := range saved.Inflight {
		if out.ID == 0 || ids[out.ID] {
			return nil
		}
		ids[out.ID] = true
		restored := &mqtt5Outbound{id: out.ID, topic: out.Topic, props: out.Props, body: out.Body}
		state.inflight = append(state.inflight, restored)
		state.inflightBytes += restored.size()
	}
	if state.inflightBytes > mqtt5MaxInflightBytes {
		return nil
	}
	return state
}
