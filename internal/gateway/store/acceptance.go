package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/pkg/irc"
)

var ErrStaleAttempt = errors.New("stale_attempt")

func (s *Store) receiptBytes(g *grantState, r *irc.SendReceiptEvent) ([]byte, error) {
	author := g.attempt.Job.Author.Nick
	data, err := core.ReceiptBytes(r, g.attempt.Job.Request, g.attempt.Job.UpstreamID, author)
	if err != nil {
		return nil, ErrInvalid
	}
	return data, nil
}

// RecordAcceptance preserves a full correlated receipt before returning a
// durable accepted result. observedAt is the backend's local observation time,
// not the server's message time. Saving may finish after expiry or revocation.
// now starts terminal retention at durable completion, never before observation.
// A failed save keeps a private in-memory copy on the grant for identical retries;
// the active marker remains, so no other attempt may be reserved in this process.
func (s *Store) RecordAcceptance(ctx context.Context, g *Grant, receipt *irc.SendReceiptEvent, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) {
		return Job{}, ErrInvalid
	}
	data, err := s.receiptBytes(state, receipt)
	if err != nil {
		return Job{}, err
	}
	if state.cardAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrConflict
	}
	if state.topicAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrConflict
	}
	if state.roomAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrConflict
	}
	if state.pinAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrConflict
	}
	if state.acceptance != nil && !bytes.Equal(state.acceptance, data) {
		return Job{}, ErrConflict
	}
	// Keep this before SQLite work. Commit failure cannot erase observed acceptance.
	state.acceptance = data
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// RecordPollAcceptance preserves the compound poll acknowledgement. Creation
// keeps its native send receipt inside the typed outcome; vote and close carry
// only the authoritative result.
func (s *Store) RecordPollAcceptance(ctx context.Context, g *Grant, ack *core.PollAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.PollCreate && state.attempt.Job.Request.Operation != core.PollVote && state.attempt.Job.Request.Operation != core.PollClose || state.acceptance != nil || state.cardAcceptance != nil || state.topicAcceptance != nil || state.roomAcceptance != nil || state.pinAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil {
		return Job{}, ErrInvalid
	}
	author := state.attempt.Job.Author.Nick
	data, err := core.PollAckBytes(ack, state.attempt.Job.Request, author)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.pollAcceptance != nil && !bytes.Equal(state.pollAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.pollAcceptance = append(state.pollAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

func (s *Store) RecordRoomAcceptance(ctx context.Context, g *Grant, ack *core.RoomAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.RoomUpdate || state.acceptance != nil || state.cardAcceptance != nil || state.topicAcceptance != nil || state.pinAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	data, err := core.RoomAckBytes(ack, state.attempt.Job.Request)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.roomAcceptance != nil && !bytes.Equal(state.roomAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.roomAcceptance = append(state.roomAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

func (s *Store) RecordPinAcceptance(ctx context.Context, g *Grant, ack *core.PinAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.PinsAdd && state.attempt.Job.Request.Operation != core.PinsRemove || state.acceptance != nil || state.cardAcceptance != nil || state.topicAcceptance != nil || state.roomAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	data, err := core.PinAckBytes(ack, state.attempt.Job.Request)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.pinAcceptance != nil && !bytes.Equal(state.pinAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.pinAcceptance = append(state.pinAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

func (s *Store) RecordActivityAcceptance(ctx context.Context, g *Grant, ack *core.ActivityAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.ActivitySet || state.acceptance != nil || state.cardAcceptance != nil || state.topicAcceptance != nil || state.roomAcceptance != nil || state.pinAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	author := state.attempt.Job.Author.Nick
	data, err := core.ActivityAckBytes(ack, state.attempt.Job.Request, author)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.activityAcceptance != nil && !bytes.Equal(state.activityAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.activityAcceptance = append(state.activityAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// RecordTopicAcceptance preserves the authenticated native topic result.
func (s *Store) RecordTopicAcceptance(ctx context.Context, g *Grant, ack *core.TopicAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		if s.closed {
			return Job{}, ErrClosed
		}
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.TopicSet || state.acceptance != nil || state.cardAcceptance != nil || state.roomAcceptance != nil || state.pinAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	author := state.attempt.Job.Author.Nick
	data, err := core.TopicAckBytes(ack, state.attempt.Job.Request, author)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.topicAcceptance != nil && !bytes.Equal(state.topicAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.topicAcceptance = append(state.topicAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// RecordCardAcceptance preserves the authenticated native card returned by a
// profile or presence mutation. Card evidence is durable independently of a
// send receipt and carries no fabricated message delivery facts.
func (s *Store) RecordCardAcceptance(ctx context.Context, g *Grant, card *core.CardAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) {
		return Job{}, ErrInvalid
	}
	if state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.ProfileUpdate && state.attempt.Job.Request.Operation != core.PresenceSet || state.acceptance != nil || state.topicAcceptance != nil || state.roomAcceptance != nil || state.pinAcceptance != nil || state.activityAcceptance != nil || state.replySignalAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	author := state.attempt.Job.Author.Nick
	data, err := core.CardAckBytes(card, state.attempt.Job.Request, author)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.cardAcceptance != nil && !bytes.Equal(state.cardAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.cardAcceptance = append(state.cardAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

func (s *Store) RecordReplySignalAcceptance(ctx context.Context, g *Grant, ack *core.ReplySignalAcknowledgement, observedAt, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || !g.state.used {
		return Job{}, ErrNotReady
	}
	state := g.state
	if observedAt.Before(state.consumedAt) || !observedAt.Before(state.attempt.Deadline) || now.Before(observedAt) || state.attempt.Kind != SendAttempt || state.attempt.Job.Request.Operation != core.ReplySignalSet && state.attempt.Job.Request.Operation != core.ReplySignalCancel || state.acceptance != nil || state.cardAcceptance != nil || state.topicAcceptance != nil || state.roomAcceptance != nil || state.pinAcceptance != nil || state.activityAcceptance != nil || state.pollAcceptance != nil {
		return Job{}, ErrInvalid
	}
	author := state.attempt.Job.Author.Nick
	data, err := core.ReplySignalAckBytes(ack, state.attempt.Job.Request, author)
	if err != nil {
		return Job{}, ErrInvalid
	}
	if state.replySignalAcceptance != nil && !bytes.Equal(state.replySignalAcceptance, data) {
		return Job{}, ErrConflict
	}
	state.replySignalAcceptance = append(state.replySignalAcceptance[:0], data...)
	state.observedAt = observedAt
	return s.saveAcceptance(ctx, state, now)
}

// RetryAcceptance saves the original private copy after a journal failure. It
// never sends and does not depend on a mutable caller-owned receipt object.
func (s *Store) RetryAcceptance(ctx context.Context, g *Grant, now time.Time) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if g == nil || g.state == nil || g.state.owner != s || g.state.acceptance == nil && g.state.cardAcceptance == nil && g.state.topicAcceptance == nil && g.state.roomAcceptance == nil && g.state.pinAcceptance == nil && g.state.activityAcceptance == nil && g.state.replySignalAcceptance == nil && g.state.pollAcceptance == nil {
		return Job{}, ErrNotReady
	}
	if now.Before(g.state.observedAt) {
		return Job{}, ErrInvalid
	}
	return s.saveAcceptance(ctx, g.state, now)
}

func (s *Store) saveAcceptance(ctx context.Context, state *grantState, now time.Time) (Job, error) {
	data := state.acceptance
	card := state.cardAcceptance != nil
	topic := state.topicAcceptance != nil
	room := state.roomAcceptance != nil
	pin := state.pinAcceptance != nil
	activity := state.activityAcceptance != nil
	replySignal := state.replySignalAcceptance != nil
	poll := state.pollAcceptance != nil
	if card {
		data = state.cardAcceptance
	} else if topic {
		data = state.topicAcceptance
	} else if room {
		data = state.roomAcceptance
	} else if pin {
		data = state.pinAcceptance
	} else if activity {
		data = state.activityAcceptance
	} else if replySignal {
		data = state.replySignalAcceptance
	} else if poll {
		data = state.pollAcceptance
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	a := state.attempt
	var active sql.NullString
	var stored []byte
	var storedCard, storedTopic []byte
	var storedRoom, storedPin, storedActivity, storedReplySignal, storedPoll []byte
	if err := tx.QueryRowContext(ctx, "SELECT attempt_id,receipt,card_outcome,topic_outcome,room_outcome,pin_outcome,activity_outcome,reply_signal_outcome,poll_outcome FROM jobs WHERE author_scope=? AND request_id=?", a.Job.Scope, a.Job.Request.RequestID).Scan(&active, &stored, &storedCard, &storedTopic, &storedRoom, &storedPin, &storedActivity, &storedReplySignal, &storedPoll); err != nil {
		return Job{}, err
	}
	if (stored != nil && storedCard != nil) || (stored != nil && storedTopic != nil) || (stored != nil && storedRoom != nil) || (stored != nil && storedPin != nil) || (stored != nil && storedActivity != nil) || (stored != nil && storedReplySignal != nil) || (stored != nil && storedPoll != nil) || (storedCard != nil && storedTopic != nil) || (storedCard != nil && storedRoom != nil) || (storedCard != nil && storedPin != nil) || (storedCard != nil && storedActivity != nil) || (storedCard != nil && storedReplySignal != nil) || (storedCard != nil && storedPoll != nil) || (storedTopic != nil && storedRoom != nil) || (storedTopic != nil && storedPin != nil) || (storedTopic != nil && storedActivity != nil) || (storedTopic != nil && storedReplySignal != nil) || (storedTopic != nil && storedPoll != nil) || (storedRoom != nil && storedPin != nil) || (storedRoom != nil && storedActivity != nil) || (storedRoom != nil && storedReplySignal != nil) || (storedRoom != nil && storedPoll != nil) || (storedPin != nil && storedActivity != nil) || (storedPin != nil && storedReplySignal != nil) || (storedPin != nil && storedPoll != nil) || (storedActivity != nil && storedReplySignal != nil) || (storedActivity != nil && storedPoll != nil) || (storedReplySignal != nil && storedPoll != nil) {
		return Job{}, ErrInvalid
	}
	if (card && (stored != nil || storedTopic != nil || storedRoom != nil || storedPin != nil || storedActivity != nil || storedReplySignal != nil || storedPoll != nil)) || (topic && (stored != nil || storedCard != nil || storedRoom != nil || storedPin != nil || storedActivity != nil || storedReplySignal != nil || storedPoll != nil)) || (room && (stored != nil || storedCard != nil || storedTopic != nil || storedPin != nil || storedActivity != nil || storedReplySignal != nil || storedPoll != nil)) || (pin && (stored != nil || storedCard != nil || storedTopic != nil || storedRoom != nil || storedActivity != nil || storedReplySignal != nil || storedPoll != nil)) || (activity && (stored != nil || storedCard != nil || storedTopic != nil || storedRoom != nil || storedPin != nil || storedReplySignal != nil || storedPoll != nil)) || (replySignal && (stored != nil || storedCard != nil || storedTopic != nil || storedRoom != nil || storedPin != nil || storedActivity != nil || storedPoll != nil)) || (poll && (stored != nil || storedCard != nil || storedTopic != nil || storedRoom != nil || storedPin != nil || storedActivity != nil || storedReplySignal != nil)) || (!card && !topic && !room && !pin && !activity && !replySignal && !poll && (storedCard != nil || storedTopic != nil || storedRoom != nil || storedPin != nil || storedActivity != nil || storedReplySignal != nil || storedPoll != nil)) {
		return Job{}, ErrConflict
	}
	if card && storedCard != nil {
		if !bytes.Equal(storedCard, data) {
			return Job{}, ErrConflict
		}
	} else if topic && storedTopic != nil {
		if !bytes.Equal(storedTopic, data) {
			return Job{}, ErrConflict
		}
	} else if room && storedRoom != nil {
		if !bytes.Equal(storedRoom, data) {
			return Job{}, ErrConflict
		}
	} else if pin && storedPin != nil {
		if !bytes.Equal(storedPin, data) {
			return Job{}, ErrConflict
		}
	} else if activity && storedActivity != nil {
		if !bytes.Equal(storedActivity, data) {
			return Job{}, ErrConflict
		}
	} else if replySignal && storedReplySignal != nil {
		if !bytes.Equal(storedReplySignal, data) {
			return Job{}, ErrConflict
		}
	} else if poll && storedPoll != nil {
		if !bytes.Equal(storedPoll, data) {
			return Job{}, ErrConflict
		}
	} else if !card && stored != nil {
		if !bytes.Equal(stored, data) {
			return Job{}, ErrConflict
		}
	} else {
		if !active.Valid || active.String != a.ID {
			return Job{}, ErrStaleAttempt
		}
		var result sql.Result
		if card {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=?,topic_outcome=NULL,room_outcome=NULL,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if topic {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=?,room_outcome=NULL,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if room {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=NULL,room_outcome=?,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if pin {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=NULL,room_outcome=NULL,pin_outcome=?,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if activity {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=NULL,room_outcome=NULL,pin_outcome=NULL,activity_outcome=?,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if replySignal {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=NULL,room_outcome=NULL,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=?,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else if poll {
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=NULL,recipient_connected=NULL,message_id=NULL,receipt=NULL,card_outcome=NULL,topic_outcome=NULL,room_outcome=NULL,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=?,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		} else {
			var saved irc.SendReceiptEvent
			if err := json.Unmarshal(data, &saved); err != nil {
				return Job{}, err
			}
			result, err = tx.ExecContext(ctx, `UPDATE jobs SET state='accepted',accepted=1,may_have_accepted=0,persisted=?,recipient_connected=?,message_id=?,receipt=?,card_outcome=NULL,topic_outcome=NULL,room_outcome=NULL,pin_outcome=NULL,activity_outcome=NULL,reply_signal_outcome=NULL,poll_outcome=NULL,terminal_at=?,last_error=NULL,attempt_id=NULL,attempt_kind=NULL WHERE author_scope=? AND request_id=? AND attempt_id=? AND attempt_kind=?`, saved.Receipt.Persisted, saved.Receipt.RecipientConnected, saved.ID, data, now.Unix(), a.Job.Scope, a.Job.Request.RequestID, a.ID, a.Kind)
		}
		if err != nil {
			return Job{}, err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return Job{}, err
		}
		if count != 1 {
			return Job{}, ErrStaleAttempt
		}
	}
	j, err := readJob(ctx, tx, a.Job.Scope, a.Job.Request.RequestID)
	if err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	j.Result.NextAction = "none"
	return j, nil
}
