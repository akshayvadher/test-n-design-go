package lending

import (
	"fmt"
	"strings"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// BorrowValidationError is returned by ParseBorrowRequest when the input
// fails validation. Reason is the first validator complaint.
type BorrowValidationError struct {
	Reason string
}

// Error implements error on a pointer receiver so errors.As resolves
// *BorrowValidationError targets through wrapping layers.
func (e *BorrowValidationError) Error() string {
	return fmt.Sprintf("Invalid borrow request: %s", e.Reason)
}

// ReserveValidationError is returned by ParseReserveRequest when the input
// fails validation.
type ReserveValidationError struct {
	Reason string
}

// Error implements error on a pointer receiver.
func (e *ReserveValidationError) Error() string {
	return fmt.Sprintf("Invalid reserve request: %s", e.Reason)
}

// ReturnLoanValidationError is returned by ParseReturnLoanRequest when
// the input fails validation.
type ReturnLoanValidationError struct {
	Reason string
}

// Error implements error on a pointer receiver.
func (e *ReturnLoanValidationError) Error() string {
	return fmt.Sprintf("Invalid return loan request: %s", e.Reason)
}

// ParseBorrowRequest trims memberId and copyId, rejects either blank, and
// returns the typed-newtype values on success. Stdlib only — no validator
// library.
func ParseBorrowRequest(memberId, copyId string) (membership.MemberId, catalog.CopyId, error) {
	trimmedMember := strings.TrimSpace(memberId)
	if trimmedMember == "" {
		return "", "", &BorrowValidationError{Reason: "memberId is required"}
	}
	trimmedCopy := strings.TrimSpace(copyId)
	if trimmedCopy == "" {
		return "", "", &BorrowValidationError{Reason: "copyId is required"}
	}
	return membership.MemberId(trimmedMember), catalog.CopyId(trimmedCopy), nil
}

// ParseReserveRequest trims memberId and bookId, rejects either blank,
// and returns the typed-newtype values on success.
func ParseReserveRequest(memberId, bookId string) (membership.MemberId, catalog.BookId, error) {
	trimmedMember := strings.TrimSpace(memberId)
	if trimmedMember == "" {
		return "", "", &ReserveValidationError{Reason: "memberId is required"}
	}
	trimmedBook := strings.TrimSpace(bookId)
	if trimmedBook == "" {
		return "", "", &ReserveValidationError{Reason: "bookId is required"}
	}
	return membership.MemberId(trimmedMember), catalog.BookId(trimmedBook), nil
}

// ParseReturnLoanRequest trims loanId and rejects blank.
func ParseReturnLoanRequest(loanId string) (LoanId, error) {
	trimmed := strings.TrimSpace(loanId)
	if trimmed == "" {
		return "", &ReturnLoanValidationError{Reason: "loanId is required"}
	}
	return LoanId(trimmed), nil
}
