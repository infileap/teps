package core

// A3: a refusal is a value, never an exception.
//
// Every operation that can decline returns one of these instead of panicking,
// aborting the stream, or silently doing something else. A client that gets a
// Refusal still has a usable connection.
type Refusal struct {
	Code      string `json:"code"`
	Reason    string `json:"reason"`
	Retryable bool   `json:"retryable"`
}

func (r *Refusal) Error() string {
	if r == nil {
		return "<nil>"
	}
	return r.Code + ": " + r.Reason
}

// The refusal vocabulary. Each code carries its own answer to "may the client
// just repeat this?", so an adapter author never has to guess.
const (
	CodeClientAuthority  = "client_authority"
	CodeNoSession        = "no_session"
	CodeStaleEpoch       = "stale_epoch"
	CodeBudgetExhausted  = "budget_exhausted"
	CodeBacklogFull      = "backlog_full"
	CodeTaintedInput     = "tainted_input"
	CodeQuotaExhausted   = "quota_exhausted"
	CodeLicenseConflict  = "license_conflict"
	CodeLeaseHeld        = "lease_held"
	CodeShadowNested     = "shadow_nested"
	CodeFrameTooLarge    = "frame_too_large"
	CodeRecoveryRequired = "recovery_required"
	CodeBadRequest       = "bad_request"
)

// retryable is the whole point of the vocabulary: one table, one reading.
var retryable = map[string]bool{
	CodeClientAuthority: false,
	CodeNoSession:       false,
	CodeStaleEpoch:      false,
	CodeBudgetExhausted: false,
	CodeBacklogFull:     true,
	CodeTaintedInput:    false,
	CodeQuotaExhausted:  false,
	CodeLicenseConflict: false,
	CodeLeaseHeld:       true,
	CodeShadowNested:    false,
	CodeFrameTooLarge:   false,
	// A chain that does not match will not match on the next attempt either.
	// A retry flag must never promise a retry that cannot succeed, so this is
	// false even though it is the most "recoverable-looking" code here.
	CodeRecoveryRequired: false,
	CodeBadRequest:       false,
}

// Refuse builds a refusal. An unknown code is treated as non-retryable, which
// is the safe default: repeating something you do not understand is worse than
// stopping.
func Refuse(code, reason string) *Refusal {
	return &Refusal{Code: code, Reason: reason, Retryable: retryable[code]}
}

// Refused reports whether a code is part of the vocabulary.
func Refused(code string) bool {
	_, ok := retryable[code]
	return ok
}
