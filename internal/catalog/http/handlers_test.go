// handlers_test.go is Slice 3's HTTP-level spec for the catalog module. The
// tests construct a real *catalog.Facade with the default in-memory
// substrates (no double substitution), wrap it in NewHandlers, mount the
// routes on a chi.NewRouter with the DomainErrorMiddleware wired in, and
// exercise each endpoint via httptest.NewRecorder + a hand-built
// *http.Request.
//
// No mocks, no testify — stdlib testing only. Assertions use json.Decoder
// against the response body so whitespace is tolerated and the assertions
// remain field-level rather than byte-level.
//
// Each test wires its OWN router and registry so the cases are
// hermetically isolated; the package-level test helpers (buildRouter,
// addBookViaFacade, …) factor the repetitive bits without leaking shared
// state.
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// sequentialIds returns a deterministic id generator so minted BookId /
// CopyId values are predictable in assertions. Mirrors the same helper in
// internal/catalog/facade_test.go (kept private here so the http test file
// owns its own substrate without reaching across package boundaries).
func sequentialIds(prefix string) func() string {
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

// silentLogger returns a slog.Logger that discards all output. Tests want
// no log noise in their output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildRouter constructs a fresh facade + handlers + chi router wired with
// the same registry the composition root uses. Returned alongside the
// facade pointer so individual tests can drive arrange-phase calls through
// it (e.g. seed a book before calling FindBook).
func buildRouter(t *testing.T) (chi.Router, *catalog.Facade) {
	t.Helper()
	logger := silentLogger()
	facade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		NewID:  sequentialIds("id"),
		Logger: logger,
	})
	registry := buildTestRegistry()
	r := chi.NewRouter()
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(r, Deps{Facade: facade, Logger: logger})
	return r, facade
}

// buildTestRegistry mirrors internal/app/wiring.go's catalog block so the
// HTTP tests verify the same mapping production wiring uses.
func buildTestRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&catalog.InvalidBookError{}, http.StatusBadRequest, "invalid_book")
	registry.Register(&catalog.InvalidCopyError{}, http.StatusBadRequest, "invalid_copy")
	registry.Register(&catalog.BookNotFoundError{}, http.StatusNotFound, "book_not_found")
	registry.Register(&catalog.CopyNotFoundError{}, http.StatusNotFound, "copy_not_found")
	registry.Register(&catalog.DuplicateIsbnError{}, http.StatusConflict, "duplicate_isbn")
	return registry
}

// send executes the request against the router and returns the recorder.
func send(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// jsonRequest builds an *http.Request whose body carries body marshalled as
// JSON. A nil body produces a request with no body.
func jsonRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	if body == nil {
		return httptest.NewRequest(method, target, nil)
	}
	buf := &bytes.Buffer{}
	if err := json.NewEncoder(buf).Encode(body); err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	req := httptest.NewRequest(method, target, buf)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// rawJSONRequest builds an *http.Request with body as raw bytes — for
// tests that need a body the json package cannot easily express (e.g.
// extra fields, malformed JSON).
func rawJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// decodeBookResponse decodes the recorder's body into a BookResponse.
func decodeBookResponse(t *testing.T, rec *httptest.ResponseRecorder) BookResponse {
	t.Helper()
	var resp BookResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode BookResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// decodeBookList decodes the recorder's body into a []BookResponse.
func decodeBookList(t *testing.T, rec *httptest.ResponseRecorder) []BookResponse {
	t.Helper()
	var resp []BookResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode []BookResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// decodeCopyResponse decodes the recorder's body into a CopyResponse.
func decodeCopyResponse(t *testing.T, rec *httptest.ResponseRecorder) CopyResponse {
	t.Helper()
	var resp CopyResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode CopyResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// decodeError decodes the recorder's body into a sharedhttp.ErrorResponse.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) sharedhttp.ErrorResponse {
	t.Helper()
	var resp sharedhttp.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode ErrorResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// assertStatus fails the test if the recorder's status differs from want.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status: got %d, want %d (body=%q)", rec.Code, want, rec.Body.String())
	}
}

// validAddBookBody returns a valid AddBookRequest body the tests reuse.
func validAddBookBody() AddBookRequest {
	return AddBookRequest{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    "978-0135957059",
	}
}

// seedBook adds a book via the facade and returns the created BookDto so
// downstream assertions can use the minted BookId.
func seedBook(t *testing.T, facade *catalog.Facade, isbn string) catalog.BookDto {
	t.Helper()
	book, err := facade.AddBook(context.Background(), catalog.NewBookDto{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    catalog.Isbn(isbn),
	})
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	return book
}

// seedCopy registers a copy under bookId and returns the minted CopyDto.
func seedCopy(t *testing.T, facade *catalog.Facade, bookId catalog.BookId) catalog.CopyDto {
	t.Helper()
	copy, err := facade.RegisterCopy(context.Background(), bookId, catalog.NewCopyDto{
		BookId:    bookId,
		Condition: catalog.CopyConditionGood,
	})
	if err != nil {
		t.Fatalf("seed copy: %v", err)
	}
	return copy
}

// -----------------------------------------------------------------------------
// POST /books
// -----------------------------------------------------------------------------

func TestAddBook_ValidBodyReturns201AndBookResponse(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, jsonRequest(t, http.MethodPost, "/books", validAddBookBody()))

	assertStatus(t, rec, http.StatusCreated)
	body := decodeBookResponse(t, rec)
	if body.BookId == "" {
		t.Errorf("BookId: got empty, want non-empty")
	}
	if got, want := body.Title, "The Pragmatic Programmer"; got != want {
		t.Errorf("Title: got %q, want %q", got, want)
	}
	if got, want := body.Isbn, "978-0135957059"; got != want {
		t.Errorf("Isbn: got %q, want %q", got, want)
	}
}

func TestAddBook_EmptyTitleReturns400InvalidBook(t *testing.T) {
	router, _ := buildRouter(t)
	req := validAddBookBody()
	req.Title = ""

	rec := send(router, jsonRequest(t, http.MethodPost, "/books", req))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_book"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestAddBook_MalformedIsbnReturns400InvalidBook(t *testing.T) {
	router, _ := buildRouter(t)
	req := validAddBookBody()
	req.Isbn = "not-an-isbn"

	rec := send(router, jsonRequest(t, http.MethodPost, "/books", req))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_book"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestAddBook_DuplicateIsbnReturns409DuplicateIsbn(t *testing.T) {
	router, facade := buildRouter(t)
	seedBook(t, facade, "978-0135957059")

	rec := send(router, jsonRequest(t, http.MethodPost, "/books", validAddBookBody()))

	assertStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Error, "duplicate_isbn"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestAddBook_UnknownFieldReturns400(t *testing.T) {
	router, _ := buildRouter(t)
	body := `{"title":"X","authors":["A"],"isbn":"978-0135957059","extra":"surprise"}`

	rec := send(router, rawJSONRequest(http.MethodPost, "/books", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_book"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// GET /books
// -----------------------------------------------------------------------------

func TestListBooks_EmptyReturns200AndEmptyArray(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodGet, "/books", nil))

	assertStatus(t, rec, http.StatusOK)
	body := bytes.TrimSpace(rec.Body.Bytes())
	if !bytes.Equal(body, []byte("[]")) {
		t.Errorf("body: got %q, want %q (must serialise empty list as [], not null)", body, "[]")
	}
	list := decodeBookList(t, rec)
	if len(list) != 0 {
		t.Errorf("decoded list: got %d entries, want 0", len(list))
	}
}

func TestListBooks_PopulatedReturnsBookList(t *testing.T) {
	router, facade := buildRouter(t)
	seedBook(t, facade, "978-0134685991")
	seedBook(t, facade, "978-0135957059")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/books", nil))

	assertStatus(t, rec, http.StatusOK)
	books := decodeBookList(t, rec)
	if got, want := len(books), 2; got != want {
		t.Fatalf("count: got %d, want %d", got, want)
	}
	if got, want := books[0].Isbn, "978-0134685991"; got != want {
		t.Errorf("books[0].Isbn: got %q, want %q", got, want)
	}
	if got, want := books[1].Isbn, "978-0135957059"; got != want {
		t.Errorf("books[1].Isbn: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// GET /books/{isbn}
// -----------------------------------------------------------------------------

func TestFindBook_KnownIsbnReturns200(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/books/978-0135957059", nil))

	assertStatus(t, rec, http.StatusOK)
	body := decodeBookResponse(t, rec)
	if got, want := body.BookId, string(seeded.BookId); got != want {
		t.Errorf("BookId: got %q, want %q", got, want)
	}
	if got, want := body.Isbn, "978-0135957059"; got != want {
		t.Errorf("Isbn: got %q, want %q", got, want)
	}
}

func TestFindBook_UnknownIsbnReturns404BookNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodGet, "/books/978-0000000000", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "book_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// PATCH /books/{bookId}
// -----------------------------------------------------------------------------

func TestUpdateBook_TitleOnlyReturns200WithUpdatedTitle(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	body := map[string]string{"title": "Updated Title"}
	rec := send(router, jsonRequest(t, http.MethodPatch, "/books/"+string(seeded.BookId), body))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeBookResponse(t, rec)
	if got, want := resp.Title, "Updated Title"; got != want {
		t.Errorf("Title: got %q, want %q", got, want)
	}
	if got, want := resp.Isbn, "978-0135957059"; got != want {
		t.Errorf("Isbn must be unchanged: got %q, want %q", got, want)
	}
}

func TestUpdateBook_WithIsbnFieldReturns400InvalidBook(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	rec := send(router, rawJSONRequest(http.MethodPatch, "/books/"+string(seeded.BookId), `{"isbn":"978-0000000000"}`))

	assertStatus(t, rec, http.StatusBadRequest)
	resp := decodeError(t, rec)
	if got, want := resp.Error, "invalid_book"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
	if !bytes.Contains([]byte(resp.Message), []byte("isbn cannot be updated")) {
		t.Errorf("message: %q does not contain %q", resp.Message, "isbn cannot be updated")
	}
}

func TestUpdateBook_EmptyBodyReturns400(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	rec := send(router, rawJSONRequest(http.MethodPatch, "/books/"+string(seeded.BookId), `{}`))

	assertStatus(t, rec, http.StatusBadRequest)
}

// -----------------------------------------------------------------------------
// DELETE /books/{bookId}
// -----------------------------------------------------------------------------

func TestDeleteBook_Returns204AndEmptyBody(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	rec := send(router, httptest.NewRequest(http.MethodDelete, "/books/"+string(seeded.BookId), nil))

	assertStatus(t, rec, http.StatusNoContent)
	if got := rec.Body.Len(); got != 0 {
		t.Errorf("body: got %d bytes, want 0 (body=%q)", got, rec.Body.String())
	}
}

func TestDeleteBook_UnknownIdReturns404(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodDelete, "/books/does-not-exist", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "book_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// POST /books/{bookId}/copies
// -----------------------------------------------------------------------------

func TestRegisterCopy_ValidConditionReturns201(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	body := NewCopyRequest{Condition: "GOOD"}
	rec := send(router, jsonRequest(t, http.MethodPost, "/books/"+string(seeded.BookId)+"/copies", body))

	assertStatus(t, rec, http.StatusCreated)
	resp := decodeCopyResponse(t, rec)
	if resp.CopyId == "" {
		t.Errorf("CopyId: got empty, want non-empty")
	}
	if got, want := resp.BookId, string(seeded.BookId); got != want {
		t.Errorf("BookId: got %q, want %q", got, want)
	}
	if got, want := resp.Condition, "GOOD"; got != want {
		t.Errorf("Condition: got %q, want %q", got, want)
	}
	if got, want := resp.Status, "AVAILABLE"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

func TestRegisterCopy_InvalidConditionReturns400InvalidCopy(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")

	body := NewCopyRequest{Condition: "MEDIOCRE"}
	rec := send(router, jsonRequest(t, http.MethodPost, "/books/"+string(seeded.BookId)+"/copies", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_copy"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestRegisterCopy_UnknownBookIdReturns404(t *testing.T) {
	router, _ := buildRouter(t)

	body := NewCopyRequest{Condition: "GOOD"}
	rec := send(router, jsonRequest(t, http.MethodPost, "/books/does-not-exist/copies", body))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "book_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// PATCH /copies/{copyId}/available + /unavailable
// -----------------------------------------------------------------------------

func TestMarkCopyAvailable_Returns200WithAvailableStatus(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")
	copy := seedCopy(t, facade, seeded.BookId)

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/copies/"+string(copy.CopyId)+"/available", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeCopyResponse(t, rec)
	if got, want := resp.Status, "AVAILABLE"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

func TestMarkCopyUnavailable_Returns200WithUnavailableStatus(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedBook(t, facade, "978-0135957059")
	copy := seedCopy(t, facade, seeded.BookId)

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/copies/"+string(copy.CopyId)+"/unavailable", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeCopyResponse(t, rec)
	if got, want := resp.Status, "UNAVAILABLE"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

func TestMarkCopyAvailable_UnknownIdReturns404CopyNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/copies/does-not-exist/available", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "copy_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}
