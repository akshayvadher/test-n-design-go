//go:build integration

package crucialpath_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/akshayvadher/test-n-design-go/test/support"
)

// registerMemberRequest mirrors internal/membership/http.RegisterMemberRequest.
// The crucial-path test re-declares the wire shape rather than importing
// it so a silent rename of the public field would break this integration
// test, which is what we want.
type registerMemberRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// memberResponse mirrors internal/membership/http.MemberResponse.
type memberResponse struct {
	MemberId string `json:"memberId"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Tier     string `json:"tier"`
	Status   string `json:"status"`
}

// upgradeTierRequest mirrors internal/membership/http.UpgradeTierRequest.
type upgradeTierRequest struct {
	Tier string `json:"tier"`
}

// eligibilityResponse mirrors internal/membership/http.EligibilityResponse.
type eligibilityResponse struct {
	MemberId string `json:"memberId"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

const (
	adaName  = "Ada"
	adaEmail = "ada@example.com"
)

// TestMembershipCrucialPath boots the full app against real Postgres +
// Redis and walks the membership HTTP surface from POST /members through
// PATCH /members/{id}/tier, asserting status codes and response bodies.
//
// Tables are truncated in t.Cleanup so subsequent crucial-path tests
// start clean.
func TestMembershipCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateMembershipTables(t, app)
	})

	var registered memberResponse

	t.Run("POST /members persists a new member with STANDARD ACTIVE defaults", func(t *testing.T) {
		req := registerMemberRequest{Name: adaName, Email: adaEmail}
		resp := postJSONMembers(t, app.BaseURL+"/members", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /members status: got %d, want %d (body=%s)", got, want, readAllMembers(t, resp.Body))
		}
		decodeJSONMembers(t, resp.Body, &registered)
		if registered.MemberId == "" {
			t.Errorf("POST /members: memberId is empty")
		}
		if registered.Tier != "STANDARD" {
			t.Errorf("POST /members tier: got %q, want %q", registered.Tier, "STANDARD")
		}
		if registered.Status != "ACTIVE" {
			t.Errorf("POST /members status: got %q, want %q", registered.Status, "ACTIVE")
		}
		if registered.Name != adaName {
			t.Errorf("POST /members name: got %q, want %q", registered.Name, adaName)
		}
		if registered.Email != adaEmail {
			t.Errorf("POST /members email: got %q, want %q", registered.Email, adaEmail)
		}
	})

	t.Run("POST /members with duplicate email returns 409 + duplicate_email", func(t *testing.T) {
		duplicate := registerMemberRequest{Name: adaName, Email: adaEmail}
		resp := postJSONMembers(t, app.BaseURL+"/members", duplicate)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("POST /members (duplicate) status: got %d, want %d", got, want)
		}
		var errBody errorResponse
		decodeJSONMembers(t, resp.Body, &errBody)
		if errBody.Error != "duplicate_email" {
			t.Errorf("POST /members (duplicate) error: got %q, want %q", errBody.Error, "duplicate_email")
		}
	})

	t.Run("GET /members/{id} returns the registered member", func(t *testing.T) {
		resp := getJSONMembers(t, app.BaseURL+"/members/"+registered.MemberId)
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /members/{id} status: got %d, want %d", got, want)
		}
		var fetched memberResponse
		decodeJSONMembers(t, resp.Body, &fetched)
		if fetched.MemberId != registered.MemberId {
			t.Errorf("GET /members/{id} memberId: got %q, want %q", fetched.MemberId, registered.MemberId)
		}
	})

	t.Run("PATCH /members/{id}/suspend then GET /eligibility returns Eligible=false Reason=SUSPENDED", func(t *testing.T) {
		suspendResp := patchMembers(t, app.BaseURL+"/members/"+registered.MemberId+"/suspend")
		defer suspendResp.Body.Close()
		if got, want := suspendResp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /suspend status: got %d, want %d", got, want)
		}

		eligibility := getJSONMembers(t, app.BaseURL+"/members/"+registered.MemberId+"/eligibility")
		defer eligibility.Body.Close()
		if got, want := eligibility.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /eligibility status: got %d, want %d", got, want)
		}
		var body eligibilityResponse
		decodeJSONMembers(t, eligibility.Body, &body)
		if body.Eligible {
			t.Errorf("Eligible: got true, want false")
		}
		if body.Reason != "SUSPENDED" {
			t.Errorf("Reason: got %q, want %q", body.Reason, "SUSPENDED")
		}
	})

	t.Run("PATCH /members/{id}/reactivate then GET /eligibility returns Eligible=true with no Reason field", func(t *testing.T) {
		reactivateResp := patchMembers(t, app.BaseURL+"/members/"+registered.MemberId+"/reactivate")
		defer reactivateResp.Body.Close()
		if got, want := reactivateResp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /reactivate status: got %d, want %d", got, want)
		}

		eligibility := getJSONMembers(t, app.BaseURL+"/members/"+registered.MemberId+"/eligibility")
		defer eligibility.Body.Close()
		raw, err := io.ReadAll(eligibility.Body)
		if err != nil {
			t.Fatalf("read /eligibility body: %v", err)
		}
		if got, want := eligibility.StatusCode, http.StatusOK; got != want {
			t.Fatalf("GET /eligibility status: got %d, want %d", got, want)
		}
		var body eligibilityResponse
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("decode /eligibility body: %v", err)
		}
		if !body.Eligible {
			t.Errorf("Eligible: got false, want true")
		}
		if bytes.Contains(raw, []byte(`"reason"`)) {
			t.Errorf("body must not contain `reason` key for active member, got %s", raw)
		}
	})

	t.Run("PATCH /members/{id}/tier with PREMIUM returns 200 + Tier=PREMIUM", func(t *testing.T) {
		req := upgradeTierRequest{Tier: "PREMIUM"}
		resp := patchJSONMembers(t, app.BaseURL+"/members/"+registered.MemberId+"/tier", req)
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /tier status: got %d, want %d (body=%s)", got, want, readAllMembers(t, resp.Body))
		}
		var body memberResponse
		decodeJSONMembers(t, resp.Body, &body)
		if body.Tier != "PREMIUM" {
			t.Errorf("PATCH /tier: got %q, want %q", body.Tier, "PREMIUM")
		}
	})
}

// postJSONMembers encodes body as JSON and POSTs it to url.
func postJSONMembers(t *testing.T, url string, body any) *http.Response {
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

// patchJSONMembers encodes body as JSON and PATCHes it to url.
func patchJSONMembers(t *testing.T, url string, body any) *http.Response {
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

// patchMembers sends a PATCH with an empty body.
func patchMembers(t *testing.T, url string) *http.Response {
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

// getJSONMembers sends an HTTP GET.
func getJSONMembers(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// decodeJSONMembers decodes body into dst.
func decodeJSONMembers(t *testing.T, body io.Reader, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}

// readAllMembers reads body to completion and returns the resulting
// string, used only on failure paths to enrich error messages.
func readAllMembers(t *testing.T, body io.Reader) string {
	t.Helper()
	raw, err := io.ReadAll(body)
	if err != nil {
		t.Logf("read body: %v", err)
		return ""
	}
	return string(raw)
}

// truncateMembershipTables clears the members table so subsequent
// crucial-path tests run on a clean substrate.
func truncateMembershipTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(), "TRUNCATE members RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate membership tables: %v", err)
	}
}
