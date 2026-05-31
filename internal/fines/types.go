// Package fines is the bounded-context module for assessing and paying
// fines on overdue loans. The Facade is the only public surface that other
// modules or the HTTP layer should depend on; the repository port, the
// in-memory + bun adapters, the schema parsers and the sample-data
// builders are exported only so composition-root wiring and same-package
// tests can reference them.
//
// Fines READS across modules synchronously (lending.ListLoansFor,
// lending.ListOverdueLoans, membership.FindMember). It WRITES only its
// own fines table and publishes its own FineAssessed +
// MemberAutoSuspended events. The single cross-module WRITE is
// membership.Suspend, called from maybeAutoSuspend AFTER the fines table
// write completes — matches the post-commit cross-module-write rule.
//
// Per the discovery doc this package depends on:
//
//   - the standard library + log/slog
//   - github.com/google/uuid
//   - internal/lending, internal/membership
//   - internal/shared/events
//
// It does NOT depend on chi, bun, viper, internal/catalog directly, or
// internal/accesscontrol: those concerns live in internal/fines/http +
// internal/fines/bun_repository.go + the composition root.
package fines

import (
	"fmt"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// FineId is the fines-internal identifier for a fine. Named so call sites
// cannot accidentally swap it with another string-keyed identifier
// (LoanId, MemberId, …) without a compiler complaint.
type FineId string

// AmountCents is the monetary type for fines. Named integer matches the
// source TS AmountCents alias and keeps arithmetic between rate, days and
// totals type-safe.
type AmountCents int64

// FinesConfig holds the two policy knobs the facade reads at runtime.
// DailyRateCents multiplies into the per-loan amount; SuspensionThresholdCents
// gates the auto-suspend trigger. Defaults: 25 / 1000 (env vars
// FINES_DAILY_RATE_CENTS, FINES_SUSPENSION_THRESHOLD_CENTS).
type FinesConfig struct {
	DailyRateCents           AmountCents
	SuspensionThresholdCents AmountCents
}

// FineDto is the canonical persisted shape of a fine. PaidAt is a pointer
// for the "unpaid" sentinel — matches the source TS optional `paidAt`
// field.
type FineDto struct {
	FineId      FineId
	MemberId    membership.MemberId
	LoanId      lending.LoanId
	AmountCents AmountCents
	AssessedAt  time.Time
	PaidAt      *time.Time
}

// FineAssessed is the domain event the facade publishes after persisting a
// fresh fine. Field order matches the source TS event 1:1.
type FineAssessed struct {
	FineId      FineId
	MemberId    membership.MemberId
	LoanId      lending.LoanId
	AmountCents AmountCents
	AssessedAt  time.Time
}

// Type implements events.DomainEvent.
func (e FineAssessed) Type() string { return "FineAssessed" }

// MemberAutoSuspended is the domain event the facade publishes when
// maybeAutoSuspend pushes the member's status to SUSPENDED because their
// unpaid fines crossed the configured threshold. Field order matches the
// source TS event 1:1.
type MemberAutoSuspended struct {
	MemberId         membership.MemberId
	TotalUnpaidCents AmountCents
	ThresholdCents   AmountCents
	SuspendedAt      time.Time
}

// Type implements events.DomainEvent.
func (e MemberAutoSuspended) Type() string { return "MemberAutoSuspended" }

// FineNotFoundError is returned by FindFine and PayFine when a lookup by
// FineId finds no record. Message matches the source TS verbatim.
type FineNotFoundError struct {
	FineId FineId
}

// Error implements error on a pointer receiver so errors.As resolves
// *FineNotFoundError targets through wrapping layers.
func (e *FineNotFoundError) Error() string {
	return fmt.Sprintf("Fine not found: %s", e.FineId)
}

// FineAlreadyPaidError is returned by PayFine when the targeted fine
// already has a non-nil PaidAt timestamp. Message matches the source TS
// verbatim.
type FineAlreadyPaidError struct {
	FineId FineId
}

// Error implements error on a pointer receiver.
func (e *FineAlreadyPaidError) Error() string {
	return fmt.Sprintf("Fine already paid: %s", e.FineId)
}
