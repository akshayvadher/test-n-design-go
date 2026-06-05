package fines

// DefaultDailyRateCents is the per-day fine rate the source TS ships with.
// The spec locks 25 cents as the Go-port default.
const DefaultDailyRateCents AmountCents = 25

// DefaultSuspensionThresholdCents is the unpaid-fines total at which
// maybeAutoSuspend pushes a member to SUSPENDED. The spec locks 1000 cents
// as the Go-port default (the source TS uses 500; the Go port diverges per
// the Phase-4 spec's locked policy).
const DefaultSuspensionThresholdCents AmountCents = 1000

// DefaultConfig returns the locked Phase-4 defaults as a fresh value. It
// is the single source of truth for the fines domain's policy defaults;
// both production wiring and test scaffolds call into it so policy
// changes propagate without per-call literals.
func DefaultConfig() FinesConfig {
	return FinesConfig{
		DailyRateCents:           DefaultDailyRateCents,
		SuspensionThresholdCents: DefaultSuspensionThresholdCents,
	}
}
