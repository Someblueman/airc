package auth

import (
	"crypto/sha256"
	"encoding/json"
	"time"

	"github.com/Someblueman/airc/internal/gateway/core"
	"github.com/Someblueman/airc/internal/gateway/syslogwire"
)

// VerifiedSyslogBatch has cryptographic coverage but still requires durable
// freshness/replay admission. It is never a transport acknowledgement.
type VerifiedSyslogBatch struct {
	Identity              Identity
	Target                core.Target
	Signer                [32]byte
	Block                 syslogwire.SignedBlock
	Started               time.Time
	Records               [][]byte
	validFrom, validUntil time.Time
	proof                 [32]byte
}

func (b *VerifiedSyslogBatch) digest() [32]byte {
	encoded, _ := json.Marshal(struct {
		Principal, Binding string
		Target             core.Target
		Signer             [32]byte
		Block              syslogwire.SignedBlock
		Started            time.Time
		Records            [][]byte
	}{b.Identity.ID(), b.Identity.Binding(), b.Target, b.Signer, b.Block, b.Started, b.Records})
	return sha256.Sum256(encoded)
}

// Valid refuses forged or altered detached batches, and rechecks the origin
// certificate's interval when application admission actually occurs.
func (b *VerifiedSyslogBatch) Valid(now time.Time) bool {
	if b == nil || b.proof == ([32]byte{}) || now.Before(b.validFrom) || !now.Before(b.validUntil) || len(b.Records) < 1 || len(b.Records) > 99 {
		return false
	}
	bytes := 0
	for _, r := range b.Records {
		if len(r) > syslogwire.MaxRecord || len(r) > SignedSyslogBufferBytes-bytes {
			return false
		}
		bytes += len(r)
	}
	return b.proof == b.digest()
}
