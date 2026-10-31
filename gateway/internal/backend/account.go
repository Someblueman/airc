package backend

import (
	"context"
	"crypto/tls"
	"errors"
	"strconv"
	"time"

	"github.com/Someblueman/airc/gateway/internal/auth"
	"github.com/Someblueman/airc/gateway/internal/core"
	"github.com/Someblueman/airc/gateway/internal/store"
	"github.com/Someblueman/airc/pkg/irc"
)

// Refusal is AIRC refusing an author's synchronous account call, with the
// stable failure a carrier reports to its client.
type Refusal struct{ Failure core.Failure }

func (r *Refusal) Error() string { return string(r.Failure.Code) }

// Account is AIRC's confirmation of an author: its nickname as AIRC spells it
// and, for a registered author, the stable account ID AIRC reported.
type Account struct {
	Nick       string `json:"nick"`
	Registered bool   `json:"registered"`
	AccountID  string `json:"account_id,omitempty"`
}

// Register forwards AIRC's REGISTER for author with its own token, as source.
// Repeating it with the same token is idempotent at AIRC. Nothing is journaled:
// the token is used for this one session and dropped.
func (b *Backend) Register(ctx context.Context, author core.Author, credential core.Credential, source string) (Account, error) {
	if !author.Registered || credential.Empty() {
		return Account{}, store.ErrInvalid
	}
	return b.account(ctx, author, credential, source, true)
}

// Identify checks author with AIRC: a registered author logs in with its
// token, a guest connects as an AIRC guest under its nickname. A bad token, a
// guest on a registered nickname, or one a connected client holds fail here,
// before any job is admitted.
func (b *Backend) Identify(ctx context.Context, author core.Author, credential core.Credential, source string) (Account, error) {
	return b.account(ctx, author, credential, source, false)
}

func (b *Backend) account(ctx context.Context, author core.Author, credential core.Credential, source string, create bool) (Account, error) {
	if b == nil || b.upstream == nil {
		return Account{}, auth.ErrForbidden
	}
	cfg, ok := b.sessionConfig(b.upstream.pinned, author, credential, source)
	if !ok {
		return Account{}, store.ErrInvalid
	}
	cfg.CreateAccount = create
	lane := author.Scope()
	if !b.acquire(lane, store.LookupAttempt) {
		return Account{}, store.ErrCapacity
	}
	defer b.release(lane, store.LookupAttempt)
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := irc.DialContext(dialCtx, cfg)
	if err != nil {
		return Account{}, accountRefusal(author, create, err)
	}
	defer client.Close()
	result := Account{Nick: client.Nick(), Registered: author.Registered}
	if author.Registered {
		result.AccountID = client.AccountID()
	}
	return result, nil
}

// accountRefusal maps AIRC's refusal the way registrationFailure does for
// jobs: a guest on a registered nickname is nick_registered, a nickname a
// connected client holds is nick_in_use, a bad token authentication_failed,
// and AIRC's creation windows and account limit capacity_exceeded: the
// windows transient, the limit final.
func accountRefusal(author core.Author, create bool, err error) error {
	var rejected *irc.RejectedError
	var inUse *irc.NickInUseError
	var verification *tls.CertificateVerificationError
	switch {
	case errors.As(err, &inUse):
		return &Refusal{core.Failure{Code: core.NickInUse, UpstreamCode: 433}}
	case errors.Is(err, irc.ErrDaemonTooOld):
		// AIRC is below this gateway's level (pkg/irc refuses it at the
		// dial): permanent until the daemon is replaced and restarted.
		return &Refusal{core.Failure{Code: core.UnsupportedOperation}}
	case errors.As(err, &verification):
		return &Refusal{core.Failure{Code: core.AuthenticationFailed}}
	case errors.As(err, &rejected):
		code, _ := strconv.Atoi(rejected.Code)
		failure := core.Failure{Code: core.AuthenticationFailed, UpstreamCode: code}
		switch {
		case rejected.Code == "437" || rejected.Throttled:
			// Rate limited by source: AIRC did not judge the credential.
			failure.Code, failure.Transient = core.CapacityExceeded, true
		case rejected.Code == "405":
			// The account limit: a retry cannot clear it.
			failure.Code = core.CapacityExceeded
		case rejected.Code == "498" && (!author.Registered || create):
			// A guest on a registered nickname, or REGISTER of a name
			// another token already holds.
			failure.Code = core.NickRegistered
		case rejected.Code == "421":
			failure.Code = core.UnsupportedOperation
		}
		return &Refusal{failure}
	}
	// Unreachable, cancelled or timed out: worth retrying later.
	return &Refusal{core.Failure{Code: core.UpstreamUnavailable, Transient: true}}
}
