package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/categories"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Handlers is the bundle of chi-bound endpoint functions for the
// categories module. It carries the facade it delegates to plus the
// slog logger the composition root passed in.
type Handlers struct {
	facade *categories.Facade
	logger *slog.Logger
}

// NewHandlers constructs a Handlers bundle.
func NewHandlers(facade *categories.Facade, logger *slog.Logger) *Handlers {
	return &Handlers{
		facade: facade,
		logger: logger,
	}
}

// CreateCategory decodes the inbound CreateCategoryRequest, delegates
// to facade.CreateCategory, and emits 201 + CategoryResponse on
// success. A decode failure (malformed JSON, unknown field) is
// translated into *InvalidCategoryError so the domain-error middleware
// returns 400 with the `invalid_category` code.
func (h *Handlers) CreateCategory(w http.ResponseWriter, r *http.Request) error {
	var req CreateCategoryRequest
	if err := decodeStrict(r, &req); err != nil {
		return &categories.InvalidCategoryError{Reason: err.Error()}
	}
	category, err := h.facade.CreateCategory(r.Context(), req.Name)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toCategoryResponse(category))
}

// ListByPrefix reads `startsWith` from the query string, delegates to
// facade.ListByPrefix (which itself enforces the not-blank rule via
// ParseStartsWith), and emits 200 + []CategoryResponse on success.
func (h *Handlers) ListByPrefix(w http.ResponseWriter, r *http.Request) error {
	startsWith := r.URL.Query().Get("startsWith")
	out, err := h.facade.ListByPrefix(r.Context(), startsWith)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toCategoryResponseSlice(out))
}

// FindCategoryById reads :id from the URL, delegates to
// facade.FindCategoryById, and emits 200 + CategoryResponse on success.
func (h *Handlers) FindCategoryById(w http.ResponseWriter, r *http.Request) error {
	id := categories.CategoryId(chi.URLParam(r, "id"))
	category, err := h.facade.FindCategoryById(r.Context(), id)
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toCategoryResponse(category))
}

// decodeStrict decodes the request body into dst with
// DisallowUnknownFields enabled. Any decode failure (malformed JSON,
// unknown field, type mismatch) returns the raw decoder error so the
// caller can wrap it in the appropriate domain error.
func decodeStrict(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}
