//go:build integration

// Package integration_test contains the cross-package smoke tests that
// exercise the full composition root against real Postgres + Redis
// containers. Files here carry the `integration` build tag so `task test`
// (no tag) skips them; only `task test:integration` (with `-tags=integration`)
// compiles them.
//
// Phase 1's only smoke is healthz: bring up containers, apply migrations,
// boot the app, probe /healthz end-to-end. The test is intentionally tiny —
// it proves the wiring works, not the handler logic (the handler has its
// own unit test in internal/app/wiring_test.go).
package integration_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/akshayvadher/test-n-design-go/test/support"
)

// healthzBody is the canonical response payload. Repeated here (not imported
// from internal/app) so the test reads as a black-box client: if internal/app
// silently changes the body, the integration test fails loudly.
const healthzBody = `{"status":"ok"}`

// TestHealthzIntegration spins up Postgres + Redis via testcontainers,
// applies migrations, boots the full app, and probes /healthz end-to-end.
//
// The test also issues a request against /does-not-exist to prove the chi
// router is wired (a hand-rolled mux that returns 200 for everything would
// pass the healthz assertion but fail this one).
//
// Targets <30s wall time on a developer laptop including testcontainers cold
// start. Most of that budget is the postgres image pull on first run; warm
// runs land in 5–8s.
func TestHealthzIntegration(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Run("GET /healthz returns 200 with the canonical JSON body", func(t *testing.T) {
		resp := mustGet(t, app.BaseURL+"/healthz")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Errorf("status: got %d, want %d", got, want)
		}

		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type: got %q, want prefix application/json", ct)
		}

		body := readAndTrim(t, resp.Body)
		if body != healthzBody {
			t.Errorf("body: got %q, want %q", body, healthzBody)
		}
	})

	t.Run("GET /does-not-exist returns 404", func(t *testing.T) {
		resp := mustGet(t, app.BaseURL+"/does-not-exist")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Errorf("status: got %d, want %d", got, want)
		}
	})
}

// mustGet issues a GET against url, failing the test if the request itself
// fails. The returned response is the caller's to close.
func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("build request for %s: %v", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// readAndTrim reads the entire body and trims surrounding whitespace so the
// assertion is byte-identical to the literal `{"status":"ok"}` — matching the
// same trim rule the Slice 2 unit test uses on the response recorder.
func readAndTrim(t *testing.T, r io.Reader) string {
	t.Helper()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return strings.TrimSpace(string(raw))
}
