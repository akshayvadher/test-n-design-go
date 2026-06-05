package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Handlers is the bundle of chi-bound endpoint functions for the membership
// module. It carries the facade it delegates to plus the slog logger the
// composition root passed in.
type Handlers struct {
	facade *membership.Facade
	logger *slog.Logger
}

// NewHandlers constructs a Handlers bundle.
func NewHandlers(facade *membership.Facade, logger *slog.Logger) *Handlers {
	return &Handlers{
		facade: facade,
		logger: logger,
	}
}

// RegisterMember decodes the inbound RegisterMemberRequest, delegates to
// facade.RegisterMember, and emits 201 + MemberResponse on success.
func (h *Handlers) RegisterMember(w http.ResponseWriter, r *http.Request) error {
	var req RegisterMemberRequest
	if err := decodeStrict(r, &req); err != nil {
		return &membership.InvalidMemberError{Reason: err.Error()}
	}
	member, err := h.facade.RegisterMember(r.Context(), fromRegisterMemberRequest(req))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toMemberResponse(member))
}

// FindMember reads :id, delegates to facade.FindMember, and emits 200 +
// MemberResponse on success.
func (h *Handlers) FindMember(w http.ResponseWriter, r *http.Request) error {
	memberId := chi.URLParam(r, "id")
	member, err := h.facade.FindMember(r.Context(), membership.MemberId(memberId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toMemberResponse(member))
}

// Suspend reads :id, delegates to facade.Suspend, and emits 200 +
// MemberResponse on success.
func (h *Handlers) Suspend(w http.ResponseWriter, r *http.Request) error {
	memberId := chi.URLParam(r, "id")
	member, err := h.facade.Suspend(r.Context(), membership.MemberId(memberId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toMemberResponse(member))
}

// Reactivate reads :id, delegates to facade.Reactivate, and emits 200 +
// MemberResponse on success.
func (h *Handlers) Reactivate(w http.ResponseWriter, r *http.Request) error {
	memberId := chi.URLParam(r, "id")
	member, err := h.facade.Reactivate(r.Context(), membership.MemberId(memberId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toMemberResponse(member))
}

// UpgradeTier reads :id, decodes UpgradeTierRequest, delegates to
// facade.UpgradeTier, and emits 200 + MemberResponse on success.
func (h *Handlers) UpgradeTier(w http.ResponseWriter, r *http.Request) error {
	memberId := chi.URLParam(r, "id")
	var req UpgradeTierRequest
	if err := decodeStrict(r, &req); err != nil {
		return &membership.InvalidMemberError{Reason: err.Error()}
	}
	member, err := h.facade.UpgradeTier(r.Context(), membership.MemberId(memberId), membership.MembershipTier(req.Tier))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toMemberResponse(member))
}

// CheckEligibility reads :id, delegates to facade.CheckEligibility, and
// emits 200 + EligibilityResponse on success.
func (h *Handlers) CheckEligibility(w http.ResponseWriter, r *http.Request) error {
	memberId := chi.URLParam(r, "id")
	eligibility, err := h.facade.CheckEligibility(r.Context(), membership.MemberId(memberId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toEligibilityResponse(eligibility))
}

// decodeStrict decodes the request body into dst with DisallowUnknownFields
// enabled. Any decode failure (malformed JSON, unknown field, type
// mismatch) returns the raw decoder error so the caller can wrap it in the
// appropriate InvalidMemberError.
func decodeStrict(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}
