// Package lending is the bounded-context module for the library's borrow,
// return and reservation flows. The Facade is the only public surface that
// other modules or the HTTP layer should depend on; the repositories, the
// in-memory adapters, the schema parsers and the sample-data builders are
// exported only so composition-root wiring and same-package tests can
// reference them.
//
// Phase 3 ships the module skeleton across Slices 3-6 (this slice declares
// types, repositories, in-memory adapters, schemas, sample data and the
// configuration constructor; Slices 4-6 add Borrow, Reserve and ReturnLoan
// respectively) and finishes with Slice 7's HTTP surface + bun repos +
// integration tests.
//
// Per .claude/BOUNDARIES.md this package depends on:
//
//   - the standard library + log/slog
//   - github.com/google/uuid
//   - internal/accesscontrol, internal/catalog, internal/membership
//   - internal/shared/events, internal/shared/tx
//
// It does NOT depend on chi, bun, viper, internal/shared/db, the cache or
// any ISBN gateway: those concerns live in internal/lending/http (Slice 7)
// and in the composition root.
package lending

import (
	"fmt"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// LoanId is the lending-internal identifier for a loan. Named so call
// sites cannot accidentally swap it with another string-keyed identifier
// (BookId, CopyId, MemberId, ReservationId) without a compiler complaint.
type LoanId string

// ReservationId is the lending-internal identifier for a reservation.
// Named for the same type-safety reason as LoanId.
type ReservationId string

// LoanDto is the canonical persisted shape of a loan. ReturnedAt is a
// pointer for the "not yet returned" sentinel — matches the source TS
// optional `returnedAt` field.
type LoanDto struct {
	LoanId     LoanId
	MemberId   membership.MemberId
	CopyId     catalog.CopyId
	BookId     catalog.BookId
	BorrowedAt time.Time
	DueDate    time.Time
	ReturnedAt *time.Time
}

// ReservationDto is the canonical persisted shape of a reservation.
// FulfilledAt is a pointer for the "not yet fulfilled" sentinel — matches
// the source TS optional `fulfilledAt` field.
type ReservationDto struct {
	ReservationId ReservationId
	MemberId      membership.MemberId
	BookId        catalog.BookId
	ReservedAt    time.Time
	FulfilledAt   *time.Time
}

// ActiveLoanWithQueuedCount pairs an active loan with the count of
// pending reservations for its book. Declared in Phase 3 for type
// stability — Phase 4's saga and reporting flows consume it.
type ActiveLoanWithQueuedCount struct {
	Loan        LoanDto
	QueuedCount int
}

// OverdueLoanReport pairs an overdue loan with the title and authors of
// its book. Declared in Phase 3 for type stability — Phase 4's reporting
// flows consume it.
type OverdueLoanReport struct {
	Loan    LoanDto
	Title   string
	Authors []string
}

// LoanOpened is the domain event the Borrow flow stages inside its own-tx.
// Field order matches the source TS event 1:1. The event publishes during
// commit (in stage order), BEFORE the post-commit catalog mutation runs.
type LoanOpened struct {
	LoanId     LoanId
	MemberId   membership.MemberId
	CopyId     catalog.CopyId
	BookId     catalog.BookId
	BorrowedAt time.Time
	DueDate    time.Time
}

// Type implements events.DomainEvent.
func (e LoanOpened) Type() string { return "LoanOpened" }

// LoanReturned is the domain event the ReturnLoan flow publishes AFTER
// the catalog mark-available runs. NOT staged — see facade.go for the
// rationale (Phase 4's auto-loan consumer must observe the consistent
// state).
type LoanReturned struct {
	LoanId     LoanId
	MemberId   membership.MemberId
	CopyId     catalog.CopyId
	BookId     catalog.BookId
	ReturnedAt time.Time
}

// Type implements events.DomainEvent.
func (e LoanReturned) Type() string { return "LoanReturned" }

// ReservationQueued is the domain event the Reserve flow stages inside
// its own-tx. Pure staged-event flow — no post-commit cross-module
// mutation.
type ReservationQueued struct {
	ReservationId ReservationId
	MemberId      membership.MemberId
	BookId        catalog.BookId
	ReservedAt    time.Time
}

// Type implements events.DomainEvent.
func (e ReservationQueued) Type() string { return "ReservationQueued" }

// ReservationFulfilled is the domain event the auto-loan saga consumer
// stages inside its claim-tx after writing FulfilledAt onto the reservation
// row. Field order matches the source TS event 1:1.
type ReservationFulfilled struct {
	ReservationId ReservationId
	MemberId      membership.MemberId
	BookId        catalog.BookId
	FulfilledAt   time.Time
}

// Type implements events.DomainEvent.
func (e ReservationFulfilled) Type() string { return "ReservationFulfilled" }

// ReservationUnfulfilled is the domain event the saga consumer stages
// inside its un-fulfil-tx after the downstream Borrow rejected. Restores
// the reservation to its pending state so the next return can claim it.
type ReservationUnfulfilled struct {
	ReservationId ReservationId
	MemberId      membership.MemberId
	BookId        catalog.BookId
	UnfulfilledAt time.Time
}

// Type implements events.DomainEvent.
func (e ReservationUnfulfilled) Type() string { return "ReservationUnfulfilled" }

// AutoLoanOpened is the domain event the saga consumer publishes
// (OUTSIDE any tx) after Borrow succeeds. Field order matches the source
// TS event 1:1: BookId, LoanId, MemberId, ReservationId, OpenedAt.
type AutoLoanOpened struct {
	BookId        catalog.BookId
	LoanId        LoanId
	MemberId      membership.MemberId
	ReservationId ReservationId
	OpenedAt      time.Time
}

// Type implements events.DomainEvent.
func (e AutoLoanOpened) Type() string { return "AutoLoanOpened" }

// AutoLoanFailed is the domain event the saga consumer publishes (OUTSIDE
// any tx) when the downstream Borrow rejected. Fires REGARDLESS of whether
// the un-fulfil tx succeeded — the failure signal is decoupled from the
// un-fulfil atomicity boundary on purpose.
type AutoLoanFailed struct {
	BookId        catalog.BookId
	ReservationId ReservationId
	MemberId      membership.MemberId
	Reason        string
	FailedAt      time.Time
}

// Type implements events.DomainEvent.
func (e AutoLoanFailed) Type() string { return "AutoLoanFailed" }

// LoanNotFoundError is returned by ReturnLoan and the (Phase 4) reporting
// flows when a lookup by LoanId finds no record.
type LoanNotFoundError struct {
	LoanId LoanId
}

// Error implements error on a pointer receiver so errors.As resolves
// *LoanNotFoundError targets through wrapping layers.
func (e *LoanNotFoundError) Error() string {
	return fmt.Sprintf("Loan not found: %s", e.LoanId)
}

// ReservationNotFoundError is returned by Phase 4's reservation lookups.
// Declared in Phase 3 so the domain-error registry can register it once
// and Phase 4 flows have somewhere to point.
type ReservationNotFoundError struct {
	ReservationId ReservationId
}

// Error implements error on a pointer receiver.
func (e *ReservationNotFoundError) Error() string {
	return fmt.Sprintf("Reservation not found: %s", e.ReservationId)
}

// CopyUnavailableError is returned by Borrow when the requested copy is
// not in CopyStatusAvailable. Message matches the source TS verbatim.
type CopyUnavailableError struct {
	CopyId catalog.CopyId
}

// Error implements error on a pointer receiver.
func (e *CopyUnavailableError) Error() string {
	return fmt.Sprintf("Copy is not available for borrowing: %s", e.CopyId)
}

// MemberIneligibleError is returned by Borrow and Reserve when the
// membership facade reports the member as ineligible. Reason carries the
// stable code the membership facade returned (e.g. "SUSPENDED"); empty
// reason is collapsed to "INELIGIBLE" at the call site per the source TS.
type MemberIneligibleError struct {
	MemberId membership.MemberId
	Reason   string
}

// Error implements error on a pointer receiver.
func (e *MemberIneligibleError) Error() string {
	return fmt.Sprintf("Member %s is not eligible to borrow: %s", e.MemberId, e.Reason)
}
