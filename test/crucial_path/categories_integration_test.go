//go:build integration

package crucialpath_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/test/support"
)

// categoryRequestBody mirrors internal/categories/http.CreateCategoryRequest —
// re-declared here so a silent rename in the wire shape breaks the
// integration test.
type categoryRequestBody struct {
	Name string `json:"name"`
}

// categoryResponseBody mirrors internal/categories/http.CategoryResponse —
// note the wire key `id`, not `categoryId`, matching the TS API.
type categoryResponseBody struct {
	Id        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}

// TestCategoriesCrucialPath boots the full app against real Postgres
// + Redis and walks the categories HTTP surface from POST through
// listing + lookup. Asserts status codes, response bodies, and row
// counts against the bun *bun.DB so we know the wiring really
// persists.
func TestCategoriesCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateCategoriesTables(t, app)
	})

	var created categoryResponseBody

	t.Run("POST /categories persists a new category and returns 201 + CategoryResponse", func(t *testing.T) {
		req := categoryRequestBody{Name: "Fiction"}
		resp := postJSON(t, app.BaseURL+"/categories", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /categories status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		decodeJSON(t, resp.Body, &created)
		if created.Id == "" {
			t.Errorf("POST /categories: id is empty")
		}
		if created.Name != "Fiction" {
			t.Errorf("POST /categories name: got %q, want %q", created.Name, "Fiction")
		}
		assertCategoriesRowCount(t, app, 1)
	})

	t.Run("POST /categories with case-insensitive duplicate name returns 409 duplicate_category", func(t *testing.T) {
		duplicate := categoryRequestBody{Name: "FICTION"}
		resp := postJSON(t, app.BaseURL+"/categories", duplicate)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("POST /categories (duplicate) status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "duplicate_category" {
			t.Errorf("error code: got %q, want %q", errBody.Error, "duplicate_category")
		}
	})

	t.Run("POST /categories with blank name returns 400 invalid_category", func(t *testing.T) {
		resp := postJSON(t, app.BaseURL+"/categories", categoryRequestBody{Name: "   "})
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
			t.Fatalf("POST /categories (blank) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "invalid_category" {
			t.Errorf("error code: got %q, want %q", errBody.Error, "invalid_category")
		}
	})

	t.Run("POST /categories with unknown JSON field returns 400 invalid_category", func(t *testing.T) {
		resp := postRawJSON(t, app.BaseURL+"/categories", `{"name":"Mystery","extra":"surprise"}`)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
			t.Fatalf("POST /categories (unknown field) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "invalid_category" {
			t.Errorf("error code: got %q, want %q", errBody.Error, "invalid_category")
		}
	})

	t.Run("GET /categories?startsWith=fi returns 200 + matching list", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/categories?startsWith=fi")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /categories status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var listed []categoryResponseBody
		decodeJSON(t, resp.Body, &listed)
		if len(listed) != 1 {
			t.Fatalf("len: got %d, want 1", len(listed))
		}
		if listed[0].Name != "Fiction" {
			t.Errorf("Name: got %q, want %q", listed[0].Name, "Fiction")
		}
	})

	t.Run("GET /categories?startsWith= returns 400 invalid_categories_query", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/categories?startsWith=")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusBadRequest; got != want {
			t.Fatalf("GET /categories (blank prefix) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "invalid_categories_query" {
			t.Errorf("error code: got %q, want %q", errBody.Error, "invalid_categories_query")
		}
	})

	t.Run("GET /categories/{id} returns 200 + CategoryResponse for a known id", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/categories/"+created.Id)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /categories/{id} status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var fetched categoryResponseBody
		decodeJSON(t, resp.Body, &fetched)
		if fetched.Id != created.Id {
			t.Errorf("Id: got %q, want %q", fetched.Id, created.Id)
		}
		if fetched.Name != "Fiction" {
			t.Errorf("Name: got %q, want %q", fetched.Name, "Fiction")
		}
	})

	t.Run("GET /categories/{id} for unknown id returns 404 category_not_found", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/categories/does-not-exist")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("GET /categories/{id} (unknown) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "category_not_found" {
			t.Errorf("error code: got %q, want %q", errBody.Error, "category_not_found")
		}
	})
}

// -----------------------------------------------------------------------------
// Helpers (specific to the categories test).
// -----------------------------------------------------------------------------

// postRawJSON POSTs a literal JSON body string (used for the
// unknown-field test where the body cannot be marshalled from a Go
// struct that omits the rogue field).
func postRawJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build POST %s: %v", url, err)
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// assertCategoriesRowCount asserts the row count of the categories
// table equals want.
func assertCategoriesRowCount(t *testing.T, app support.BootedApp, want int) {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM categories")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count categories: %v", err)
	}
	if got != want {
		t.Errorf("categories row count: got %d, want %d", got, want)
	}
}

// truncateCategoriesTables clears the categories table between runs.
func truncateCategoriesTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(),
		"TRUNCATE categories RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate categories tables: %v", err)
	}
}
