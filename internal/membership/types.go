// Package membership is the bounded-context module for the library's
// memberships. The Facade is the only public surface that other modules or
// the HTTP layer should depend on; the repository, the in-memory adapter,
// the schema parser and the sample-data builders are exported only so
// composition-root wiring and same-package tests can reference them.
//
// Phase 2 ships the full membership module in one slice — facade, tests,
// HTTP surface, and bun-backed Postgres repository — because the module is
// thinner than catalog (no cache, no ISBN gateway, no copy-status state
// machine) and there is no new architectural ground to break.
//
// The package depends on the standard library + log/slog + github.com/google/uuid
// only. It does NOT depend on chi, bun, viper or any HTTP framework: those
// concerns live in internal/membership/http and in the composition root.
package membership

import "fmt"

// MemberId is the membership-internal identifier for a member. Named so
// call sites cannot accidentally swap it with another string-keyed
// identifier (BookId, CopyId, …) without a compiler complaint.
type MemberId string

// MembershipTier is the closed set of membership tiers. The values match
// the source TS MembershipTier union 1:1.
type MembershipTier string

// MembershipTier values. These are the only MembershipTier literals the
// rest of the codebase should reference — never raw string literals.
const (
	MembershipTierStandard MembershipTier = "STANDARD"
	MembershipTierPremium  MembershipTier = "PREMIUM"
)

// MembershipStatus is the closed set of membership statuses. The values
// match the source TS MembershipStatus union 1:1.
type MembershipStatus string

// MembershipStatus values.
const (
	MembershipStatusActive    MembershipStatus = "ACTIVE"
	MembershipStatusSuspended MembershipStatus = "SUSPENDED"
)

// NewMemberDto is the inbound shape for registering a member. Both fields
// are trimmed and validated by ParseNewMember before the facade uses them.
type NewMemberDto struct {
	Name  string
	Email string
}

// MemberDto is the canonical persisted shape of a member.
type MemberDto struct {
	MemberId MemberId
	Name     string
	Email    string
	Tier     MembershipTier
	Status   MembershipStatus
}

// EligibilityDto is the outbound shape for CheckEligibility. Reason carries
// a stable code (e.g. "SUSPENDED") when Eligible is false; for an eligible
// member Reason is the zero value and the HTTP layer omits it via
// omitempty.
type EligibilityDto struct {
	MemberId MemberId
	Eligible bool
	Reason   string
}

// MemberNotFoundError is returned when a lookup by MemberId finds no
// record. Identifier is the raw string the caller supplied so the surfaced
// error names the missing thing.
type MemberNotFoundError struct {
	Identifier string
}

// Error implements error on a pointer receiver so errors.As resolves
// *MemberNotFoundError targets through wrapping layers.
func (e *MemberNotFoundError) Error() string {
	return fmt.Sprintf("Member not found: %s", e.Identifier)
}

// DuplicateEmailError is returned by RegisterMember when a member with the
// same email already exists in the repository.
type DuplicateEmailError struct {
	Email string
}

func (e *DuplicateEmailError) Error() string {
	return fmt.Sprintf("A member with email %s already exists", e.Email)
}

// InvalidMemberError is returned by ParseNewMember when the input fails
// validation. Reason is the validator's first failure message.
type InvalidMemberError struct {
	Reason string
}

func (e *InvalidMemberError) Error() string {
	return fmt.Sprintf("Invalid member: %s", e.Reason)
}
