package store

import "github.com/Someblueman/airc/internal/gateway/core"

// These are scheduling hints, not grants; Reserve still checks current
// authorization, expiry, the due time and exclusive ownership atomically.
func setNextAction(j *Job, sendLimit, lookupLimit int, nextSend, nextLookup int64, lookupForSend int) {
	r := &j.Result
	r.NextAction = "none"
	if r.State == core.Dispatching {
		r.NextAction = "await_receipt"
		return
	}
	if r.State == core.CredentialWait {
		r.NextAction = "resubmit_credential"
		return
	}
	if r.State == core.Accepted || r.State == core.Expired || r.State == core.Blocked {
		return
	}
	canLookup := r.SendAttempts > 0 && r.LookupAttempts < lookupLimit && (j.Policy == core.ReceiptOnly || r.SendAttempts >= sendLimit || r.State == core.Rejected || lookupForSend < r.SendAttempts)
	if canLookup && r.AcceptanceUncertain {
		r.NextAction, r.NextAttemptAt = "lookup", &nextLookup
		return
	}
	if r.State == core.Rejected {
		return
	}
	if r.SendAttempts < sendLimit && (j.Policy == core.Retry || r.SendAttempts == 0) {
		r.NextAction, r.NextAttemptAt = "send", &nextSend
	}
}
