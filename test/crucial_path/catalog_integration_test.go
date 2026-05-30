//go:build integration

// Package crucialpath_test is the Phase-2 crucial-path test suite: each file
// here boots the full composition root via test/support.BootApp against
// testcontainers Postgres + Redis and exercises one module's HTTP surface
// end-to-end. Crucial-path tests are the only place where "the wiring
// works against real infrastructure" is asserted; per-module unit tests
// stay in their package and use in-memory substrates.
//
// Files here carry the `integration` build tag so `task test` skips them;
// only `task test:integration` (with `-tags=integration`) compiles them.
package crucialpath_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akshayvadher/test-n-design-go/test/support"
)

// bookRequest mirrors internal/catalog/http.AddBookRequest. The crucial-path
// test re-declares the wire shape (rather than importing it) so the test
// reads as an HTTP client — a silent rename of the public field would
// break the integration test, which is what we want.
type bookRequest struct {
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// bookResponse mirrors internal/catalog/http.BookResponse.
type bookResponse struct {
	BookId  string   `json:"bookId"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// updateBookRequest mirrors internal/catalog/http.UpdateBookRequest. Only
// the fields the crucial-path test exercises are present.
type updateBookRequest struct {
	Title *string `json:"title,omitempty"`
}

// newCopyRequest mirrors internal/catalog/http.NewCopyRequest.
type newCopyRequest struct {
	Condition string `json:"condition"`
}

// copyResponse mirrors internal/catalog/http.CopyResponse.
type copyResponse struct {
	CopyId    string `json:"copyId"`
	BookId    string `json:"bookId"`
	Condition string `json:"condition"`
	Status    string `json:"status"`
}

// errorResponse mirrors internal/shared/http.ErrorResponse.
type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

const (
	pragmaticIsbn    = "978-0135957059"
	pragmaticTitle   = "The Pragmatic Programmer"
	pragmaticAuthor1 = "Andrew Hunt"
	pragmaticAuthor2 = "David Thomas"
)

// TestCatalogCrucialPath boots the full app against real Postgres + Redis
// and walks the catalog HTTP surface from POST /books through DELETE
// /books, asserting status codes, response bodies, and row counts directly
// against the bun *bun.DB so we know the wiring really persists.
//
// Tables are truncated in t.Cleanup so subsequent crucial-path tests
// (membership in Slice 5) start clean. The container is shared across the
// suite via testcontainers' lifecycle bound to the outer test.
func TestCatalogCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateCatalogTables(t, app)
	})

	var added bookResponse

	t.Run("POST /books persists a new book and a follow-up GET returns it", func(t *testing.T) {
		req := bookRequest{
			Title:   pragmaticTitle,
			Authors: []string{pragmaticAuthor1, pragmaticAuthor2},
			Isbn:    pragmaticIsbn,
		}
		resp := postJSON(t, app.BaseURL+"/books", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /books status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		decodeJSON(t, resp.Body, &added)
		if added.BookId == "" {
			t.Errorf("POST /books: bookId is empty")
		}
		if added.Title != pragmaticTitle {
			t.Errorf("POST /books title: got %q, want %q", added.Title, pragmaticTitle)
		}
		if len(added.Authors) != 2 || added.Authors[0] != pragmaticAuthor1 || added.Authors[1] != pragmaticAuthor2 {
			t.Errorf("POST /books authors: got %v, want [%q %q]", added.Authors, pragmaticAuthor1, pragmaticAuthor2)
		}
		if added.Isbn != pragmaticIsbn {
			t.Errorf("POST /books isbn: got %q, want %q", added.Isbn, pragmaticIsbn)
		}

		got := getJSON(t, app.BaseURL+"/books/"+pragmaticIsbn)
		defer got.Body.Close()
		if status := got.StatusCode; status != http.StatusOK {
			t.Fatalf("GET /books/{isbn} status: got %d, want %d", status, http.StatusOK)
		}
		var fetched bookResponse
		decodeJSON(t, got.Body, &fetched)
		if fetched.BookId != added.BookId {
			t.Errorf("GET /books/{isbn} bookId: got %q, want %q", fetched.BookId, added.BookId)
		}

		assertBooksRowCount(t, app, pragmaticIsbn, 1)
	})

	t.Run("POST /books with duplicate isbn returns 409 + duplicate_isbn", func(t *testing.T) {
		duplicate := bookRequest{
			Title:   pragmaticTitle,
			Authors: []string{pragmaticAuthor1},
			Isbn:    pragmaticIsbn,
		}
		resp := postJSON(t, app.BaseURL+"/books", duplicate)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("POST /books (duplicate) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "duplicate_isbn" {
			t.Errorf("POST /books (duplicate) error: got %q, want %q", errBody.Error, "duplicate_isbn")
		}
	})

	t.Run("PATCH /books/{bookId} updates the title and GET reflects it", func(t *testing.T) {
		newTitle := "The Pragmatic Programmer, 20th Anniversary Edition"
		patch := updateBookRequest{Title: &newTitle}
		resp := patchJSON(t, app.BaseURL+"/books/"+added.BookId, patch)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /books status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var patched bookResponse
		decodeJSON(t, resp.Body, &patched)
		if patched.Title != newTitle {
			t.Errorf("PATCH /books title: got %q, want %q", patched.Title, newTitle)
		}

		got := getJSON(t, app.BaseURL+"/books/"+pragmaticIsbn)
		defer got.Body.Close()
		var fetched bookResponse
		decodeJSON(t, got.Body, &fetched)
		if fetched.Title != newTitle {
			t.Errorf("GET /books/{isbn} title after PATCH: got %q, want %q", fetched.Title, newTitle)
		}
	})

	var registeredCopy copyResponse

	t.Run("POST /books/{bookId}/copies registers an AVAILABLE copy", func(t *testing.T) {
		copyReq := newCopyRequest{Condition: "GOOD"}
		resp := postJSON(t, app.BaseURL+"/books/"+added.BookId+"/copies", copyReq)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /copies status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		decodeJSON(t, resp.Body, &registeredCopy)
		if registeredCopy.CopyId == "" {
			t.Errorf("POST /copies: copyId is empty")
		}
		if registeredCopy.Status != "AVAILABLE" {
			t.Errorf("POST /copies status: got %q, want %q", registeredCopy.Status, "AVAILABLE")
		}
	})

	t.Run("PATCH /copies/{copyId}/unavailable then /available toggles status", func(t *testing.T) {
		unavailable := patch(t, app.BaseURL+"/copies/"+registeredCopy.CopyId+"/unavailable")
		defer unavailable.Body.Close()
		if got, want := unavailable.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /unavailable status: got %d, want %d", got, want)
		}
		var flipped copyResponse
		decodeJSON(t, unavailable.Body, &flipped)
		if flipped.Status != "UNAVAILABLE" {
			t.Errorf("PATCH /unavailable status: got %q, want %q", flipped.Status, "UNAVAILABLE")
		}

		available := patch(t, app.BaseURL+"/copies/"+registeredCopy.CopyId+"/available")
		defer available.Body.Close()
		if got, want := available.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /available status: got %d, want %d", got, want)
		}
		var restored copyResponse
		decodeJSON(t, available.Body, &restored)
		if restored.Status != "AVAILABLE" {
			t.Errorf("PATCH /available status: got %q, want %q", restored.Status, "AVAILABLE")
		}
	})

	t.Run("DELETE /books/{bookId} succeeds after copies are removed and GET returns 404", func(t *testing.T) {
		// The catalog facade does not cascade copies on DeleteBook (matches TS
		// source) and the FK constraint on copies.book_id would otherwise
		// reject the delete. The crucial-path test mirrors the same workflow
		// a client would follow: drop the copy first, then delete the book.
		deleteCopyDirect(t, app, registeredCopy.CopyId)

		resp := delete_(t, app.BaseURL+"/books/"+added.BookId)
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusNoContent; got != want {
			t.Fatalf("DELETE /books status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}

		got := getJSON(t, app.BaseURL+"/books/"+pragmaticIsbn)
		defer got.Body.Close()
		if status := got.StatusCode; status != http.StatusNotFound {
			t.Errorf("GET /books/{isbn} after delete: got %d, want 404", status)
		}

		assertBooksRowCount(t, app, pragmaticIsbn, 0)
	})
}

// postJSON encodes body as JSON and POSTs it to url with content-type
// application/json. The returned response is the caller's to close.
func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body for POST %s: %v", url, err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// patchJSON encodes body as JSON and PATCHes it to url.
func patchJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal body for PATCH %s: %v", url, err)
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("build PATCH %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

// patch sends a PATCH with an empty body. Used for routes that take no
// JSON payload (the copy-status flip endpoints).
func patch(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPatch, url, nil)
	if err != nil {
		t.Fatalf("build PATCH %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

// delete_ sends an HTTP DELETE. Named with a trailing underscore because
// `delete` is a Go built-in.
func delete_(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("build DELETE %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE %s: %v", url, err)
	}
	return resp
}

// getJSON sends an HTTP GET.
func getJSON(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// decodeJSON decodes the body into dst, failing the test on any decode
// error so the caller can rely on dst being populated.
func decodeJSON(t *testing.T, body io.Reader, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// readAll reads body to completion and returns the resulting string, used
// only on failure paths to enrich error messages.
func readAll(t *testing.T, body io.Reader) string {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Logf("read body: %v", err)
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// assertBooksRowCount runs `SELECT COUNT(*) FROM books WHERE isbn = ?`
// against the wired *bun.DB and asserts the result equals want. Exposes
// the row-level introspection the spec AC requires: HTTP-driven writes
// actually persist to Postgres.
func assertBooksRowCount(t *testing.T, app support.BootedApp, isbn string, want int) {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM books WHERE isbn = ?", isbn)
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count books WHERE isbn=%q: %v", isbn, err)
	}
	if got != want {
		t.Errorf("books row count for isbn=%q: got %d, want %d", isbn, got, want)
	}
}

// deleteCopyDirect removes a copy row from the database directly. The
// catalog HTTP surface does not expose a DELETE /copies endpoint in
// Phase 2 (the TS source does not either), but the DELETE /books flow
// needs the copy gone before the FK constraint allows the book to drop.
// Direct deletion keeps the test focused on the book lifecycle without
// pulling in lending semantics that land in Phase 3.
func deleteCopyDirect(t *testing.T, app support.BootedApp, copyId string) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(), "DELETE FROM copies WHERE copy_id = ?", copyId)
	if err != nil {
		t.Fatalf("delete copy %q directly: %v", copyId, err)
	}
}

// truncateCatalogTables clears the catalog tables so subsequent crucial-path
// tests run on a clean substrate. RESTART IDENTITY is a no-op for UUID
// primary keys but kept for symmetry with future tables that adopt serial
// keys. CASCADE drops dependent rows in `copies` before truncating
// `books`, removing the need for the test to track copy lifetimes.
func truncateCatalogTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(), "TRUNCATE books, copies RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate catalog tables: %v", err)
	}
}
