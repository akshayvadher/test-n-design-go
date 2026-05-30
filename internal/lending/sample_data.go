package lending

import (
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// LoanOption mutates a LoanDto produced by SampleNewLoan or
// SampleReturnedLoan. Options apply in the order they are passed — a
// later option overwrites an earlier one with the same target field.
type LoanOption func(*LoanDto)

// WithLoanId overrides the LoanId.
func WithLoanId(loanId LoanId) LoanOption {
	return func(dto *LoanDto) {
		dto.LoanId = loanId
	}
}

// WithLoanMemberId overrides the MemberId.
func WithLoanMemberId(memberId membership.MemberId) LoanOption {
	return func(dto *LoanDto) {
		dto.MemberId = memberId
	}
}

// WithLoanCopyId overrides the CopyId.
func WithLoanCopyId(copyId catalog.CopyId) LoanOption {
	return func(dto *LoanDto) {
		dto.CopyId = copyId
	}
}

// WithLoanBookId overrides the BookId.
func WithLoanBookId(bookId catalog.BookId) LoanOption {
	return func(dto *LoanDto) {
		dto.BookId = bookId
	}
}

// WithBorrowedAt overrides the BorrowedAt timestamp.
func WithBorrowedAt(borrowedAt time.Time) LoanOption {
	return func(dto *LoanDto) {
		dto.BorrowedAt = borrowedAt
	}
}

// WithDueDate overrides the DueDate timestamp.
func WithDueDate(dueDate time.Time) LoanOption {
	return func(dto *LoanDto) {
		dto.DueDate = dueDate
	}
}

// WithReturnedAt sets the ReturnedAt pointer to a fresh copy of the
// supplied time. Useful for tests that exercise the returned-loan path.
func WithReturnedAt(returnedAt time.Time) LoanOption {
	return func(dto *LoanDto) {
		copied := returnedAt
		dto.ReturnedAt = &copied
	}
}

// SampleNewLoan returns a LoanDto defaulted to placeholder ids and a
// deterministic borrow/due window, mutated by the supplied options in
// order. The defaults match the source TS sample-loan shape.
func SampleNewLoan(opts ...LoanOption) LoanDto {
	dto := LoanDto{
		LoanId:     "loan-placeholder-id",
		MemberId:   "member-placeholder-id",
		CopyId:     "copy-placeholder-id",
		BookId:     "book-placeholder-id",
		BorrowedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		DueDate:    time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
		ReturnedAt: nil,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}

// SampleReturnedLoan returns a LoanDto pre-populated with a ReturnedAt
// pointer (so tests exercising the returned-loan path don't restate it),
// mutated by the supplied options in order.
func SampleReturnedLoan(opts ...LoanOption) LoanDto {
	returnedAt := time.Date(2024, 1, 10, 0, 0, 0, 0, time.UTC)
	dto := SampleNewLoan()
	dto.ReturnedAt = &returnedAt
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}

// ReservationOption mutates a ReservationDto produced by
// SampleNewReservation.
type ReservationOption func(*ReservationDto)

// WithReservationId overrides the ReservationId.
func WithReservationId(reservationId ReservationId) ReservationOption {
	return func(dto *ReservationDto) {
		dto.ReservationId = reservationId
	}
}

// WithReservationMemberId overrides the MemberId.
func WithReservationMemberId(memberId membership.MemberId) ReservationOption {
	return func(dto *ReservationDto) {
		dto.MemberId = memberId
	}
}

// WithReservationBookId overrides the BookId.
func WithReservationBookId(bookId catalog.BookId) ReservationOption {
	return func(dto *ReservationDto) {
		dto.BookId = bookId
	}
}

// WithReservedAt overrides the ReservedAt timestamp.
func WithReservedAt(reservedAt time.Time) ReservationOption {
	return func(dto *ReservationDto) {
		dto.ReservedAt = reservedAt
	}
}

// WithFulfilledAt sets the FulfilledAt pointer to a fresh copy of the
// supplied time.
func WithFulfilledAt(fulfilledAt time.Time) ReservationOption {
	return func(dto *ReservationDto) {
		copied := fulfilledAt
		dto.FulfilledAt = &copied
	}
}

// SampleNewReservation returns a ReservationDto defaulted to placeholder
// ids, a deterministic ReservedAt and a nil FulfilledAt, mutated by the
// supplied options in order.
func SampleNewReservation(opts ...ReservationOption) ReservationDto {
	dto := ReservationDto{
		ReservationId: "reservation-placeholder-id",
		MemberId:      "member-placeholder-id",
		BookId:        "book-placeholder-id",
		ReservedAt:    time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
		FulfilledAt:   nil,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}
