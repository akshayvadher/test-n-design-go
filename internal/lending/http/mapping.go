package http

import (
	"github.com/akshayvadher/test-n-design-go/internal/lending"
)

// toLoanResponse translates a lending.LoanDto into the outbound HTTP DTO.
// String casts unwrap the LoanId / MemberId / CopyId / BookId named types
// so JSON encoding sees plain strings. ReturnedAt's pointer propagates
// as-is so json:"returnedAt,omitempty" strips the field when nil.
func toLoanResponse(loan lending.LoanDto) LoanResponse {
	return LoanResponse{
		LoanId:     string(loan.LoanId),
		MemberId:   string(loan.MemberId),
		CopyId:     string(loan.CopyId),
		BookId:     string(loan.BookId),
		BorrowedAt: loan.BorrowedAt,
		DueDate:    loan.DueDate,
		ReturnedAt: loan.ReturnedAt,
	}
}

// toReservationResponse translates a lending.ReservationDto into the
// outbound HTTP DTO.
func toReservationResponse(reservation lending.ReservationDto) ReservationResponse {
	return ReservationResponse{
		ReservationId: string(reservation.ReservationId),
		MemberId:      string(reservation.MemberId),
		BookId:        string(reservation.BookId),
		ReservedAt:    reservation.ReservedAt,
		FulfilledAt:   reservation.FulfilledAt,
	}
}
