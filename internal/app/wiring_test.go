package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// -----------------------------------------------------------------------------
// AC: GET /healthz returns 200 + application/json + body `{"status":"ok"}`
// (byte-identical after bytes.TrimSpace). This is the unit-level cover for the
// handler that internal/app/wiring.go mounts on the chi router. The full
// integration smoke (real listener, real testcontainers Postgres + Redis,
// real migrations) lives in test/integration/healthz_integration_test.go.
// -----------------------------------------------------------------------------

func TestHealthzHandler_RespondsWithJSONStatusOK(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	healthzHandler(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Errorf("status code: got %d, want %d", got, want)
	}

	if got, want := resp.Header.Get("Content-Type"), "application/json"; got != want {
		t.Errorf("Content-Type: got %q, want %q", got, want)
	}

	body := bytes.TrimSpace(rec.Body.Bytes())
	if want := []byte(`{"status":"ok"}`); !bytes.Equal(body, want) {
		t.Errorf("body: got %q, want %q", body, want)
	}
}
