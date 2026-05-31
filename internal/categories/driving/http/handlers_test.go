// handlers_test.go is the HTTP-level spec for the categories module.
// The tests construct a real *categories.Facade with the default
// in-memory substrates, wrap it in NewHandlers, mount the routes on a
// chi.NewRouter with the DomainErrorMiddleware wired in, and exercise
// each endpoint via httptest.NewRecorder + a hand-built *http.Request.
//
// No mocks, no testify — stdlib testing only.
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
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/categories"
	categoriesmemory "github.com/akshayvadher/test-n-design-go/internal/categories/driven/memory"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func sequentialIds(prefix string) func() string {
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func buildRouter(t *testing.T) (chi.Router, *categories.Facade) {
	t.Helper()
	logger := silentLogger()
	facade := categoriesmemory.NewFacadeWithOverrides(categoriesmemory.Overrides{
		NewID:  sequentialIds("cat"),
		Clock:  func() time.Time { return time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC) },
		Logger: logger,
	})
	registry := buildTestRegistry()
	r := chi.NewRouter()
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(r, Deps{Facade: facade, Logger: logger})
	return r, facade
}

func buildTestRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&categories.InvalidCategoryError{}, http.StatusBadRequest, "invalid_category")
	registry.Register(&categories.InvalidCategoriesQueryError{}, http.StatusBadRequest, "invalid_categories_query")
	registry.Register(&categories.CategoryNotFoundError{}, http.StatusNotFound, "category_not_found")
	registry.Register(&categories.DuplicateCategoryError{}, http.StatusConflict, "duplicate_category")
	return registry
}

func send(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

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

func rawJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func decodeCategoryResponse(t *testing.T, rec *httptest.ResponseRecorder) CategoryResponse {
	t.Helper()
	var resp CategoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode CategoryResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

func decodeCategoryResponseSlice(t *testing.T, rec *httptest.ResponseRecorder) []CategoryResponse {
	t.Helper()
	var resp []CategoryResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode []CategoryResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) sharedhttp.ErrorResponse {
	t.Helper()
	var resp sharedhttp.ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode ErrorResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status: got %d, want %d (body=%q)", rec.Code, want, rec.Body.String())
	}
}

func seedCategory(t *testing.T, facade *categories.Facade, name string) categories.CategoryDto {
	t.Helper()
	category, err := facade.CreateCategory(context.Background(), name)
	if err != nil {
		t.Fatalf("seed category %q: %v", name, err)
	}
	return category
}

// -----------------------------------------------------------------------------
// POST /categories
// -----------------------------------------------------------------------------

func TestCreateCategory_ValidBodyReturns201AndCategoryResponse(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, jsonRequest(t, http.MethodPost, "/categories", CreateCategoryRequest{Name: "Fiction"}))

	assertStatus(t, rec, http.StatusCreated)
	body := decodeCategoryResponse(t, rec)
	if body.Id == "" {
		t.Errorf("Id: got empty, want non-empty")
	}
	if body.Name != "Fiction" {
		t.Errorf("Name: got %q, want %q", body.Name, "Fiction")
	}
}

func TestCreateCategory_BlankNameReturns400InvalidCategory(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, jsonRequest(t, http.MethodPost, "/categories", CreateCategoryRequest{Name: "   "}))

	assertStatus(t, rec, http.StatusBadRequest)
	if got := decodeError(t, rec).Error; got != "invalid_category" {
		t.Errorf("error code: got %q, want %q", got, "invalid_category")
	}
}

func TestCreateCategory_DuplicateNameReturns409DuplicateCategory(t *testing.T) {
	router, facade := buildRouter(t)
	seedCategory(t, facade, "Fiction")

	rec := send(router, jsonRequest(t, http.MethodPost, "/categories", CreateCategoryRequest{Name: "FICTION"}))

	assertStatus(t, rec, http.StatusConflict)
	if got := decodeError(t, rec).Error; got != "duplicate_category" {
		t.Errorf("error code: got %q, want %q", got, "duplicate_category")
	}
}

func TestCreateCategory_UnknownFieldReturns400(t *testing.T) {
	router, _ := buildRouter(t)
	body := `{"name":"Fiction","extra":"surprise"}`

	rec := send(router, rawJSONRequest(http.MethodPost, "/categories", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got := decodeError(t, rec).Error; got != "invalid_category" {
		t.Errorf("error code: got %q, want %q", got, "invalid_category")
	}
}

// -----------------------------------------------------------------------------
// GET /categories
// -----------------------------------------------------------------------------

func TestListByPrefix_ReturnsMatchingCategoriesSortedAsc(t *testing.T) {
	router, facade := buildRouter(t)
	seedCategory(t, facade, "Apple")
	seedCategory(t, facade, "art")
	seedCategory(t, facade, "Banana")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/categories?startsWith=a", nil))

	assertStatus(t, rec, http.StatusOK)
	matches := decodeCategoryResponseSlice(t, rec)
	if len(matches) != 2 {
		t.Fatalf("len: got %d, want 2", len(matches))
	}
	if matches[0].Name != "Apple" || matches[1].Name != "art" {
		t.Errorf("names: got [%q,%q], want [Apple, art]", matches[0].Name, matches[1].Name)
	}
}

func TestListByPrefix_BlankPrefixReturns400InvalidCategoriesQuery(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodGet, "/categories?startsWith=", nil))

	assertStatus(t, rec, http.StatusBadRequest)
	if got := decodeError(t, rec).Error; got != "invalid_categories_query" {
		t.Errorf("error code: got %q, want %q", got, "invalid_categories_query")
	}
}

// -----------------------------------------------------------------------------
// GET /categories/{id}
// -----------------------------------------------------------------------------

func TestFindCategoryById_KnownIdReturns200(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedCategory(t, facade, "Fiction")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/categories/"+string(seeded.CategoryId), nil))

	assertStatus(t, rec, http.StatusOK)
	body := decodeCategoryResponse(t, rec)
	if body.Id != string(seeded.CategoryId) {
		t.Errorf("Id: got %q, want %q", body.Id, seeded.CategoryId)
	}
	if body.Name != "Fiction" {
		t.Errorf("Name: got %q, want %q", body.Name, "Fiction")
	}
}

func TestFindCategoryById_UnknownIdReturns404CategoryNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodGet, "/categories/does-not-exist", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got := decodeError(t, rec).Error; got != "category_not_found" {
		t.Errorf("error code: got %q, want %q", got, "category_not_found")
	}
}
