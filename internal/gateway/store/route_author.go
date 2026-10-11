package store

import (
	"context"
	"database/sql"
)

// routeOwner resolves the author a provider-verified route posts as. Provider
// replay tables (SNMP, signed syslog) record its author scope, so an event or
// origin stays bound to the author that first used it.
func (s *Store) routeOwner(sub Submission) (submitter, error) {
	return s.authorSubmitter(sub)
}

// readOwnedJob reads owner's job, or ErrNotFound when it is absent or not
// owner's (a registered author's job needs the matching token).
func (s *Store) readOwnedJob(ctx context.Context, tx *sql.Tx, owner submitter, requestID string) (Job, error) {
	j, err := readJob(ctx, tx, owner.scope, requestID)
	if err != nil {
		return Job{}, err
	}
	if owner.owns(j) != nil {
		return Job{}, ErrNotFound
	}
	return j, nil
}
