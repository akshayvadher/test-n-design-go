// handlers.go wires the chi-bound endpoint functions for the fines
// module. Handlers are demo-auth: memberId / fineId come from URL path
// params with no auth middleware in front (matching the Phase-2/3
// demo-auth shortcut).
package http

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Handlers is the bundle of chi-bound endpoint functions for the fines
// module. It carries the facade it delegates to, the slog logger the
// composition root passed in, and the clock the AssessFinesFor /
// ProcessOverdueLoans endpoints read `now` through (injected so tests
// drive `now` deterministically).
type Handlers struct {
	facade *fines.Facade
	logger *slog.Logger
	clock  func() time.Time
}

// NewHandlers constructs a Handlers bundle. Clock nil substitutes
// time.Now.
func NewHandlers(facade *fines.Facade, logger *slog.Logger, clock func() time.Time) *Handlers {
	if clock == nil {
		clock = time.Now
	}
	return &Handlers{
		facade: facade,
		logger: logger,
		clock:  clock,
	}
}

// AssessFinesFor reads :memberId, delegates to facade.AssessFinesFor with
// the handler's clock-resolved now, and emits 200 + []FineResponse on
// success. 200 (not 201) matches the source TS @HttpCode(200) on a POST.
func (h *Handlers) AssessFinesFor(w http.ResponseWriter, r *http.Request) error {
	memberId := membership.MemberId(chi.URLParam(r, "memberId"))
	assessed, err := h.facade.AssessFinesFor(r.Context(), memberId, h.clock())
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toFineResponseSlice(assessed))
}

// ProcessOverdueLoans delegates to facade.ProcessOverdueLoans and emits
// 204 (no body) on success.
func (h *Handlers) ProcessOverdueLoans(w http.ResponseWriter, r *http.Request) error {
	if err := h.facade.ProcessOverdueLoans(r.Context(), h.clock()); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ListFinesFor reads :memberId, delegates to facade.ListFinesFor, and
// emits 200 + []FineResponse on success.
func (h *Handlers) ListFinesFor(w http.ResponseWriter, r *http.Request) error {
	memberId := membership.MemberId(chi.URLParam(r, "memberId"))
	out, err := h.facade.ListFinesFor(r.Context(), memberId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toFineResponseSlice(out))
}

// FindFine reads :fineId, validates via fines.ParseFineId, delegates to
// facade.FindFine, and emits 200 + FineResponse on success.
func (h *Handlers) FindFine(w http.ResponseWriter, r *http.Request) error {
	fineId, err := fines.ParseFineId(chi.URLParam(r, "fineId"))
	if err != nil {
		return err
	}
	fine, err := h.facade.FindFine(r.Context(), fineId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toFineResponse(fine))
}

// PayFine reads :fineId, validates via fines.ParseFineId, delegates to
// facade.PayFine, and emits 200 + FineResponse on success.
func (h *Handlers) PayFine(w http.ResponseWriter, r *http.Request) error {
	fineId, err := fines.ParseFineId(chi.URLParam(r, "fineId"))
	if err != nil {
		return err
	}
	fine, err := h.facade.PayFine(r.Context(), fineId)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toFineResponse(fine))
}
