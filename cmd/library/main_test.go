// main_test.go covers the pure http.HandlerFunc symbols defined in main.go.
//
// Slice 2's end-to-end shutdown sequencing (signal.NotifyContext +
// http.Server.ListenAndServe + Shutdown) is deferred to Slice 7's integration
// smoke test — it cannot be observed without spinning up real OS signals and
// a real listener, both of which are integration concerns. This file is the
// unit-test slice: the JSON shape of GET /healthz.
package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// -----------------------------------------------------------------------------
// AC: GET /healthz returns 200 + application/json + body `{"status":"ok"}`
// (byte-identical after bytes.TrimSpace).
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
