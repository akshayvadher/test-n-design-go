package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// Handlers is the bundle of chi-bound endpoint functions for the catalog
// module. It carries the facade it delegates to plus the slog logger the
// composition root passed in. Construction is via NewHandlers; the module's
// Wire function takes the bundle and mounts each method on the router.
type Handlers struct {
	facade *catalog.Facade
	logger *slog.Logger
}

// NewHandlers constructs a Handlers bundle. The facade is required and is
// the only thing the handlers depend on; the logger is held so future
// observability work can attach per-handler log lines without changing the
// constructor signature.
func NewHandlers(facade *catalog.Facade, logger *slog.Logger) *Handlers {
	return &Handlers{
		facade: facade,
		logger: logger,
	}
}

// AddBook decodes the inbound AddBookRequest, delegates to facade.AddBook,
// and emits 201 + BookResponse on success. Domain errors bubble up to the
// DomainErrorMiddleware via the sharedhttp.Handle wrapper.
func (h *Handlers) AddBook(w http.ResponseWriter, r *http.Request) error {
	var req AddBookRequest
	if err := decodeStrict(r, &req); err != nil {
		return &catalog.InvalidBookError{Reason: err.Error()}
	}
	book, err := h.facade.AddBook(r.Context(), fromAddBookRequest(req))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toBookResponse(book))
}

// ListBooks emits 200 + []BookResponse. An empty repository serialises as
// `[]` rather than `null` because the slice is initialised with make().
func (h *Handlers) ListBooks(w http.ResponseWriter, r *http.Request) error {
	books, err := h.facade.ListBooks(r.Context())
	if err != nil {
		return err
	}
	responses := make([]BookResponse, 0, len(books))
	for _, book := range books {
		responses = append(responses, toBookResponse(book))
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, responses)
}

// FindBook reads the :isbn URL param, delegates to facade.FindBook, and
// emits 200 + BookResponse on success.
func (h *Handlers) FindBook(w http.ResponseWriter, r *http.Request) error {
	isbn := chi.URLParam(r, "isbn")
	book, err := h.facade.FindBook(r.Context(), catalog.Isbn(isbn))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toBookResponse(book))
}

// UpdateBook reads the :bookId URL param, decodes the UpdateBookRequest
// (rejecting unknown fields including `isbn`), delegates to
// facade.UpdateBook, and emits 200 + BookResponse on success.
func (h *Handlers) UpdateBook(w http.ResponseWriter, r *http.Request) error {
	bookId := chi.URLParam(r, "bookId")
	var req UpdateBookRequest
	if err := decodeStrict(r, &req); err != nil {
		return &catalog.InvalidBookError{Reason: rejectIsbnUpdateReason(err)}
	}
	book, err := h.facade.UpdateBook(r.Context(), catalog.BookId(bookId), fromUpdateBookRequest(req))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toBookResponse(book))
}

// DeleteBook reads :bookId, delegates to facade.DeleteBook, and emits 204
// with an empty body on success.
func (h *Handlers) DeleteBook(w http.ResponseWriter, r *http.Request) error {
	bookId := chi.URLParam(r, "bookId")
	if err := h.facade.DeleteBook(r.Context(), catalog.BookId(bookId)); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// RegisterCopy reads :bookId, decodes NewCopyRequest, delegates to
// facade.RegisterCopy, and emits 201 + CopyResponse on success.
func (h *Handlers) RegisterCopy(w http.ResponseWriter, r *http.Request) error {
	bookId := chi.URLParam(r, "bookId")
	var req NewCopyRequest
	if err := decodeStrict(r, &req); err != nil {
		return &catalog.InvalidCopyError{Reason: err.Error()}
	}
	copy, err := h.facade.RegisterCopy(r.Context(), catalog.BookId(bookId), catalog.NewCopyDto{
		BookId:    catalog.BookId(bookId),
		Condition: catalog.CopyCondition(req.Condition),
	})
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusCreated, toCopyResponse(copy))
}

// MarkCopyAvailable reads :copyId, delegates to facade.MarkCopyAvailable,
// and emits 200 + CopyResponse on success.
func (h *Handlers) MarkCopyAvailable(w http.ResponseWriter, r *http.Request) error {
	copyId := chi.URLParam(r, "copyId")
	copy, err := h.facade.MarkCopyAvailable(r.Context(), catalog.CopyId(copyId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toCopyResponse(copy))
}

// MarkCopyUnavailable reads :copyId, delegates to facade.MarkCopyUnavailable,
// and emits 200 + CopyResponse on success.
func (h *Handlers) MarkCopyUnavailable(w http.ResponseWriter, r *http.Request) error {
	copyId := chi.URLParam(r, "copyId")
	copy, err := h.facade.MarkCopyUnavailable(r.Context(), catalog.CopyId(copyId))
	if err != nil {
		return err
	}
	return sharedhttp.WriteJSON(w, http.StatusOK, toCopyResponse(copy))
}

// decodeStrict decodes the request body into dst with DisallowUnknownFields
// enabled. Any decode failure (malformed JSON, unknown field, type
// mismatch) returns the raw decoder error so the caller can wrap it in the
// appropriate InvalidXxxError.
func decodeStrict(r *http.Request, dst any) error {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

// rejectIsbnUpdateReason produces a stable, human-readable reason when the
// caller tries to PATCH the ISBN. The TS source phrases this as "isbn
// cannot be updated"; we keep the same wording so cross-port tooling
// matches. For non-isbn decode errors we surface the underlying decoder
// message unchanged.
func rejectIsbnUpdateReason(err error) string {
	message := err.Error()
	if isIsbnUnknownFieldError(message) {
		return "isbn cannot be updated"
	}
	return message
}

// isIsbnUnknownFieldError reports whether err's message is the canonical
// encoding/json "unknown field" complaint scoped to the `isbn` field. The
// stdlib emits the message verbatim as `json: unknown field "isbn"`.
func isIsbnUnknownFieldError(message string) bool {
	return message == `json: unknown field "isbn"`
}
