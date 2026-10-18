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

// mqtt5SnapshotSessionBytes is what a sessions snapshot may take per
// session the listener keeps: its unacknowledged messages and the rest.
const mqtt5SnapshotSessionBytes = 512 << 10

// mqtt5Snapshot is a listener's sessions written to its state file, so
// they outlive a gateway restart. Wills are not kept: they hold their
// author's credential.
type mqtt5Snapshot struct {
	Version  int                    `json:"version"`
	Sessions []mqtt5SnapshotSession `json:"sessions"`
	Groups   []mqtt5SnapshotGroup   `json:"groups,omitempty"`
}

// mqtt5SnapshotGroup is a shared subscription group's position.
type mqtt5SnapshotGroup struct {
	Owner  string    `json:"owner"`
	Name   string    `json:"name"`
	Kind   mqtt5Kind `json:"kind"`
	Target string    `json:"target"`
	After  string    `json:"after"`
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
	Group  string    `json:"group,omitempty"`
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
			saved.Subs = append(saved.Subs, mqtt5SnapshotSub{Filter: sub.filter, Kind: sub.kind, Target: sub.target, QoS: sub.qos, After: sub.after, Group: sub.group})
		}
		for _, out := range state.inflight {
			saved.Inflight = append(saved.Inflight, mqtt5SnapshotOutbound{ID: out.id, Topic: out.topic, Props: out.props, Body: out.body})
		}
		state.mu.Unlock()
		snapshot.Sessions = append(snapshot.Sessions, saved)
	}
	for _, g := range s.groups {
		snapshot.Groups = append(snapshot.Groups, mqtt5SnapshotGroup{Owner: g.owner, Name: g.name, Kind: g.kind, Target: g.target, After: g.after})
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
	if !os.SameFile(link, info) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > int64(s.maxSessions)*mqtt5SnapshotSessionBytes {
		return errors.New("MQTT sessions file must be a private regular file")
	}
	var snapshot mqtt5Snapshot
	if err := json.NewDecoder(io.LimitReader(file, int64(s.maxSessions)*mqtt5SnapshotSessionBytes)).Decode(&snapshot); err != nil {
		return err
	}
	if snapshot.Version != 1 {
		return errors.New("unknown MQTT sessions file version")
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, saved := range snapshot.Sessions {
		if saved.ClientID == "" || s.sessions[saved.ClientID] != nil || saved.Owner == "" || !now.Before(saved.ExpiresAt) || saved.Expiry <= 0 || saved.Expiry > mqtt5MaxSessionExpiry || len(s.sessions) >= s.maxSessions || s.ownedLocked(saved.Owner) >= s.perOwner {
			continue
		}
		if state := restoreMQTT5Session(saved); state != nil {
			s.sessions[saved.ClientID] = state
		}
	}
	for _, saved := range snapshot.Groups {
		key := mqtt5GroupKey(saved.Owner, saved.Name, saved.Kind, saved.Target)
		if s.groups[key] == nil && s.subscribedLocked(key) {
			s.groups[key] = &mqtt5Group{owner: saved.Owner, name: saved.Name, kind: saved.Kind, target: saved.Target, after: saved.After}
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
		if reason != mqttSuccess || parsed.kind != sub.Kind || parsed.target != sub.Target || parsed.group != sub.Group || sub.QoS > 1 || state.subs[sub.Filter] != nil {
			return nil
		}
		state.subs[sub.Filter] = &mqtt5Sub{filter: sub.Filter, kind: sub.Kind, target: sub.Target, qos: sub.QoS, after: sub.After, group: sub.Group}
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
