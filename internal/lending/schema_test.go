// schema_test.go covers Slice 3's hand-written parsers: ParseBorrowRequest,
// ParseReserveRequest, ParseReturnLoanRequest. Stdlib testing only; the file
// lives in package lending so it can reference the unexported helpers if a
// future regression needs to.
package lending

import (
	"errors"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// -----------------------------------------------------------------------------
// ParseBorrowRequest — happy path (trims both inputs, returns newtyped values)
// and rejections (blank memberId, blank copyId, whitespace-only inputs).
// -----------------------------------------------------------------------------

func TestParseBorrowRequest_HappyPath(t *testing.T) {
	memberId, copyId, err := ParseBorrowRequest("  m-1  ", "  c-1  ")
	if err != nil {
		t.Fatalf("ParseBorrowRequest: got error %v, want nil", err)
	}
	if memberId != membership.MemberId("m-1") {
		t.Errorf("memberId: got %q, want %q (trim)", memberId, "m-1")
	}
	if copyId != catalog.CopyId("c-1") {
		t.Errorf("copyId: got %q, want %q (trim)", copyId, "c-1")
	}
}

func TestParseBorrowRequest_BlankMemberId(t *testing.T) {
	_, _, err := ParseBorrowRequest("", "c-1")
	var validation *BorrowValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseBorrowRequest: got %T %v, want *BorrowValidationError", err, err)
	}
	if validation.Reason != "memberId is required" {
		t.Errorf("Reason: got %q, want %q", validation.Reason, "memberId is required")
	}
}

func TestParseBorrowRequest_WhitespaceMemberId(t *testing.T) {
	_, _, err := ParseBorrowRequest("   ", "c-1")
	var validation *BorrowValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseBorrowRequest: got %T %v, want *BorrowValidationError", err, err)
	}
}

func TestParseBorrowRequest_BlankCopyId(t *testing.T) {
	_, _, err := ParseBorrowRequest("m-1", "")
	var validation *BorrowValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseBorrowRequest: got %T %v, want *BorrowValidationError", err, err)
	}
	if validation.Reason != "copyId is required" {
		t.Errorf("Reason: got %q, want %q", validation.Reason, "copyId is required")
	}
}

// -----------------------------------------------------------------------------
// ParseReserveRequest — happy path + rejections; same shape as
// ParseBorrowRequest because the validators differ only in the second field
// (copyId vs bookId).
// -----------------------------------------------------------------------------

func TestParseReserveRequest_HappyPath(t *testing.T) {
	memberId, bookId, err := ParseReserveRequest("  m-1  ", "  b-1  ")
	if err != nil {
		t.Fatalf("ParseReserveRequest: got error %v, want nil", err)
	}
	if memberId != membership.MemberId("m-1") {
		t.Errorf("memberId: got %q, want %q (trim)", memberId, "m-1")
	}
	if bookId != catalog.BookId("b-1") {
		t.Errorf("bookId: got %q, want %q (trim)", bookId, "b-1")
	}
}

func TestParseReserveRequest_BlankMemberId(t *testing.T) {
	_, _, err := ParseReserveRequest("", "b-1")
	var validation *ReserveValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseReserveRequest: got %T %v, want *ReserveValidationError", err, err)
	}
	if validation.Reason != "memberId is required" {
		t.Errorf("Reason: got %q, want %q", validation.Reason, "memberId is required")
	}
}

func TestParseReserveRequest_BlankBookId(t *testing.T) {
	_, _, err := ParseReserveRequest("m-1", "   ")
	var validation *ReserveValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseReserveRequest: got %T %v, want *ReserveValidationError", err, err)
	}
	if validation.Reason != "bookId is required" {
		t.Errorf("Reason: got %q, want %q", validation.Reason, "bookId is required")
	}
}

// -----------------------------------------------------------------------------
// ParseReturnLoanRequest — happy path + blank rejection.
// -----------------------------------------------------------------------------

func TestParseReturnLoanRequest_HappyPath(t *testing.T) {
	loanId, err := ParseReturnLoanRequest("  l-1  ")
	if err != nil {
		t.Fatalf("ParseReturnLoanRequest: got error %v, want nil", err)
	}
	if loanId != LoanId("l-1") {
		t.Errorf("loanId: got %q, want %q (trim)", loanId, "l-1")
	}
}

func TestParseReturnLoanRequest_BlankRejected(t *testing.T) {
	_, err := ParseReturnLoanRequest("   ")
	var validation *ReturnLoanValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("ParseReturnLoanRequest: got %T %v, want *ReturnLoanValidationError", err, err)
	}
	if validation.Reason != "loanId is required" {
		t.Errorf("Reason: got %q, want %q", validation.Reason, "loanId is required")
	}
}

// -----------------------------------------------------------------------------
// Error-message format — locked down so HTTP middleware mapping (Phase 3
// Slice 7) keeps working as the registry maps error type → status code.
// -----------------------------------------------------------------------------

func TestBorrowValidationError_MessageFormat(t *testing.T) {
	err := &BorrowValidationError{Reason: "memberId is required"}
	want := "Invalid borrow request: memberId is required"
	if err.Error() != want {
		t.Errorf("Error: got %q, want %q", err.Error(), want)
	}
}

func TestReserveValidationError_MessageFormat(t *testing.T) {
	err := &ReserveValidationError{Reason: "bookId is required"}
	want := "Invalid reserve request: bookId is required"
	if err.Error() != want {
		t.Errorf("Error: got %q, want %q", err.Error(), want)
	}
}

func TestReturnLoanValidationError_MessageFormat(t *testing.T) {
	err := &ReturnLoanValidationError{Reason: "loanId is required"}
	want := "Invalid return loan request: loanId is required"
	if err.Error() != want {
		t.Errorf("Error: got %q, want %q", err.Error(), want)
	}
}
