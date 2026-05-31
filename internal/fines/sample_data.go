package fines

import (
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// FineOption mutates a FineDto produced by SampleNewFine. Options apply in
// the order they are passed — a later option overwrites an earlier one with
// the same target field (last-option-wins).
type FineOption func(*FineDto)

// WithFineId overrides the FineId.
func WithFineId(fineId FineId) FineOption {
	return func(dto *FineDto) {
		dto.FineId = fineId
	}
}

// WithFineMemberId overrides the MemberId.
func WithFineMemberId(memberId membership.MemberId) FineOption {
	return func(dto *FineDto) {
		dto.MemberId = memberId
	}
}

// WithFineLoanId overrides the LoanId.
func WithFineLoanId(loanId lending.LoanId) FineOption {
	return func(dto *FineDto) {
		dto.LoanId = loanId
	}
}

// WithAmountCents overrides the AmountCents.
func WithAmountCents(amount AmountCents) FineOption {
	return func(dto *FineDto) {
		dto.AmountCents = amount
	}
}

// WithAssessedAt overrides the AssessedAt timestamp.
func WithAssessedAt(assessedAt time.Time) FineOption {
	return func(dto *FineDto) {
		dto.AssessedAt = assessedAt
	}
}

// WithPaidAt sets the PaidAt pointer to a fresh copy of the supplied time.
// Useful for tests that exercise the paid-fine path without going through
// the facade's PayFine call.
func WithPaidAt(paidAt time.Time) FineOption {
	return func(dto *FineDto) {
		copied := paidAt
		dto.PaidAt = &copied
	}
}

// SampleNewFine returns a FineDto defaulted to placeholder ids, the source
// TS sample assessment timestamp and an unpaid sentinel, mutated by the
// supplied options in order.
func SampleNewFine(opts ...FineOption) FineDto {
	dto := FineDto{
		FineId:      "fine-placeholder-id",
		MemberId:    "member-placeholder-id",
		LoanId:      "loan-placeholder-id",
		AmountCents: 100,
		AssessedAt:  time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC),
		PaidAt:      nil,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}
