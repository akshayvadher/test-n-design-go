package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Handlers is the bundle of chi-bound endpoint functions for the lending
// module. It carries the facade it delegates to plus the slog logger the
// composition root passed in.
type Handlers struct {
	facade *lending.Facade
	logger *slog.Logger
}

// NewHandlers constructs a Handlers bundle. The facade is required and is
// the only thing the handlers depend on; the logger is held so future
// observability work can attach per-handler log lines without changing the
// constructor signature.
func NewHandlers(facade *lending.Facade, logger *slog.Logger) *Handlers {
	return &Handlers{
		facade: facade,
		logger: logger,
	}
}

// Borrow decodes the inbound BorrowRequest, validates via
// lending.ParseBorrowRequest, builds the demo-auth AuthUser with role
// MEMBER (matching the source TS demo shortcut — no JWT, no session), and
// delegates to facade.Borrow. Emits 201 + LoanResponse on success; domain
// errors bubble up to the DomainErrorMiddleware via sharedhttp.Handle.
func (h *Handlers) Borrow(w http.ResponseWriter, r *http.Request) error {
	var req BorrowRequest
	if err := decodeStrict(r, &req); err != nil {
		return &lending.BorrowValidationError{Reason: err.Error()}
	}
	memberId, copyId, err := lending.ParseBorrowRequest(req.MemberId, req.CopyId)
	if err != nil {
		return err
	}
	authUser := accesscontrol.AuthUser{
		MemberID: string(memberId),
		Role:     accesscontrol.RoleMember,
	}
	loan, err := h.facade.Borrow(r.Context(), authUser, copyId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toLoanResponse(loan))
}

// Reserve decodes the inbound ReserveRequest, validates via
// lending.ParseReserveRequest, and delegates to facade.Reserve. Emits 201 +
// ReservationResponse on success.
func (h *Handlers) Reserve(w http.ResponseWriter, r *http.Request) error {
	var req ReserveRequest
	if err := decodeStrict(r, &req); err != nil {
		return &lending.ReserveValidationError{Reason: err.Error()}
	}
	memberId, bookId, err := lending.ParseReserveRequest(req.MemberId, req.BookId)
	if err != nil {
		return err
	}
	reservation, err := h.facade.Reserve(r.Context(), memberId, bookId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toReservationResponse(reservation))
}

// ReturnLoan reads :loanId from the URL, validates via
// lending.ParseReturnLoanRequest, and delegates to facade.ReturnLoan.
// Emits 200 + LoanResponse with non-nil ReturnedAt on success.
func (h *Handlers) ReturnLoan(w http.ResponseWriter, r *http.Request) error {
	loanId, err := lending.ParseReturnLoanRequest(chi.URLParam(r, "loanId"))
	if err != nil {
		return err
	}
	loan, err := h.facade.ReturnLoan(r.Context(), loanId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toLoanResponse(loan))
}

// decodeStrict decodes the request body into dst with DisallowUnknownFields
// enabled. Any decode failure (malformed JSON, unknown field, type
// mismatch) returns the raw decoder error so the caller can wrap it in the
// appropriate validation error.
func decodeStrict(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}
