package store

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/Someblueman/airc/internal/gateway/matrixwire"
)

const maxMatrixProgressOwners = 128

// MatrixProgress retains bounded sync and room-history progress, never event
// bodies or credentials. Cursor is the last fully processed global sync token.
type MatrixProgress struct {
	Revision      int64
	Cursor        string
	Next          string
	HistoryCursor string
	RoomIndex     int
	Rejected      int64
}

func validMatrixProgressOwner(owner string) bool {
	if len(owner) != 64 {
		return false
	}
	for _, c := range owner {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validMatrixProgressFields(progress MatrixProgress) bool {
	return progress.Rejected >= 0 && progress.RoomIndex >= 0 && progress.RoomIndex <= 128 &&
		(progress.Cursor == "" || matrixwire.Cursor(progress.Cursor)) &&
		(progress.Next == "" || matrixwire.Cursor(progress.Next)) &&
		(progress.HistoryCursor == "" || matrixwire.Cursor(progress.HistoryCursor)) &&
		(progress.Next != "" && progress.Next != progress.Cursor || progress.Next == "" && progress.RoomIndex == 0 && progress.HistoryCursor == "")
}

func validMatrixProgress(progress MatrixProgress) bool {
	return progress.Revision >= 1 && validMatrixProgressFields(progress)
}

func (s *Store) MatrixProgress(ctx context.Context, owner string) (MatrixProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MatrixProgress{}, ErrClosed
	}
	if !validMatrixProgressOwner(owner) {
		return MatrixProgress{}, ErrInvalid
	}
	progress, _, err := readMatrixProgressRow(ctx, s.db, owner)
	return progress, err
}

// SaveMatrixProgress compares the complete previous state; the zero value
// means the owner must not yet have a row. The store mutex and journal lock
// serialize cooperating owners, while the SQL transaction supplies CAS.
func (s *Store) SaveMatrixProgress(ctx context.Context, owner string, previous, next MatrixProgress) (MatrixProgress, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return MatrixProgress{}, ErrClosed
	}
	if !validMatrixProgressOwner(owner) || (previous != (MatrixProgress{}) && !validMatrixProgress(previous)) || !validMatrixProgressFields(next) || previous.Revision == math.MaxInt64 {
		return MatrixProgress{}, ErrInvalid
	}
	if next.Rejected < previous.Rejected {
		return MatrixProgress{}, ErrInvalid
	}
	next.Revision = previous.Revision + 1
	if !validMatrixProgress(next) {
		return MatrixProgress{}, ErrInvalid
	}
	if err := s.checkpoint(ctx); err != nil {
		return MatrixProgress{}, err
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return MatrixProgress{}, err
	}
	defer tx.Rollback()
	current, exists, err := readMatrixProgressRow(ctx, tx, owner)
	if err != nil {
		return MatrixProgress{}, err
	}
	if previous == (MatrixProgress{}) {
		if exists {
			return MatrixProgress{}, ErrConflict
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM matrix_progress").Scan(&count); err != nil {
			return MatrixProgress{}, err
		}
		if count >= maxMatrixProgressOwners {
			return MatrixProgress{}, ErrCapacity
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO matrix_progress(owner,revision,cursor,next_cursor,history_cursor,room_index,rejected) VALUES(?,?,?,?,?,?,?)`, owner, next.Revision, next.Cursor, next.Next, next.HistoryCursor, next.RoomIndex, next.Rejected)
	} else {
		if !exists || current != previous {
			return MatrixProgress{}, ErrConflict
		}
		result, updateErr := tx.ExecContext(ctx, `UPDATE matrix_progress SET revision=?,cursor=?,next_cursor=?,history_cursor=?,room_index=?,rejected=?
WHERE owner=? AND revision=? AND cursor=? AND next_cursor=? AND history_cursor=? AND room_index=? AND rejected=?`,
			next.Revision, next.Cursor, next.Next, next.HistoryCursor, next.RoomIndex, next.Rejected,
			owner, previous.Revision, previous.Cursor, previous.Next, previous.HistoryCursor, previous.RoomIndex, previous.Rejected)
		if updateErr != nil {
			err = updateErr
		} else if changed, rowsErr := result.RowsAffected(); rowsErr != nil {
			err = rowsErr
		} else if changed != 1 {
			return MatrixProgress{}, ErrConflict
		}
	}
	if err != nil {
		return MatrixProgress{}, err
	}
	if err := commit(ctx, tx); err != nil {
		return MatrixProgress{}, err
	}
	return next, nil
}

func readMatrixProgressRow(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, owner string) (MatrixProgress, bool, error) {
	var progress MatrixProgress
	err := q.QueryRowContext(ctx, `SELECT revision,cursor,next_cursor,history_cursor,room_index,rejected FROM matrix_progress WHERE owner=?`, owner).
		Scan(&progress.Revision, &progress.Cursor, &progress.Next, &progress.HistoryCursor, &progress.RoomIndex, &progress.Rejected)
	if errors.Is(err, sql.ErrNoRows) {
		return MatrixProgress{}, false, nil
	}
	if err != nil {
		return MatrixProgress{}, false, err
	}
	if !validMatrixProgress(progress) {
		return MatrixProgress{}, false, ErrInvalid
	}
	return progress, true, nil
}
