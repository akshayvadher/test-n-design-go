// handlers_test.go is the HTTP-level spec for the membership module. The
// tests construct a real *membership.Facade with the default in-memory
// substrates, wrap it in NewHandlers, mount the routes on a chi.NewRouter
// with the DomainErrorMiddleware wired in, and exercise each endpoint via
// httptest.NewRecorder + a hand-built *http.Request.
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

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// sequentialIds returns a deterministic id generator so minted MemberId
// values are predictable in assertions.
func sequentialIds(prefix string) func() string {
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + strconv.Itoa(counter)
	}
}

// silentLogger returns a slog.Logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildRouter constructs a fresh facade + handlers + chi router wired with
// the same registry the composition root uses. Returned alongside the
// facade pointer so individual tests can drive arrange-phase calls through
// it.
func buildRouter(t *testing.T) (chi.Router, *membership.Facade) {
	t.Helper()
	logger := silentLogger()
	facade := membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{
		NewID:  sequentialIds("m"),
		Logger: logger,
	})
	registry := buildTestRegistry()
	r := chi.NewRouter()
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(r, Deps{Facade: facade, Logger: logger})
	return r, facade
}

// buildTestRegistry mirrors internal/app/wiring.go's membership block so
// the HTTP tests verify the same mapping production wiring uses.
func buildTestRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&membership.InvalidMemberError{}, http.StatusBadRequest, "invalid_member")
	registry.Register(&membership.MemberNotFoundError{}, http.StatusNotFound, "member_not_found")
	registry.Register(&membership.DuplicateEmailError{}, http.StatusConflict, "duplicate_email")
	return registry
}

// send executes the request against the router and returns the recorder.
func send(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// jsonRequest builds an *http.Request whose body carries body marshalled
// as JSON.
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

// rawJSONRequest builds an *http.Request with body as raw bytes.
func rawJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// decodeMemberResponse decodes the recorder's body into a MemberResponse.
func decodeMemberResponse(t *testing.T, rec *httptest.ResponseRecorder) MemberResponse {
	t.Helper()
	var resp MemberResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode MemberResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// decodeEligibilityResponse decodes the recorder's body into an
// EligibilityResponse.
func decodeEligibilityResponse(t *testing.T, rec *httptest.ResponseRecorder) EligibilityResponse {
	t.Helper()
	var resp EligibilityResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode EligibilityResponse: %v (body=%q)", err, rec.Body.String())
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

// validRegisterBody returns a valid RegisterMemberRequest body the tests
// reuse.
func validRegisterBody() RegisterMemberRequest {
	return RegisterMemberRequest{
		Name:  "Ada Lovelace",
		Email: "ada.lovelace@example.com",
	}
}

// seedMember registers a member via the facade and returns the created
// MemberDto so downstream assertions can use the minted MemberId.
func seedMember(t *testing.T, facade *membership.Facade, email string) membership.MemberDto {
	t.Helper()
	member, err := facade.RegisterMember(context.Background(), membership.NewMemberDto{
		Name:  "Ada Lovelace",
		Email: email,
	})
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	return member
}

// -----------------------------------------------------------------------------
// POST /members
// -----------------------------------------------------------------------------

func TestRegisterMember_ValidBodyReturns201AndMemberResponse(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, jsonRequest(t, http.MethodPost, "/members", validRegisterBody()))

	assertStatus(t, rec, http.StatusCreated)
	body := decodeMemberResponse(t, rec)
	if body.MemberId == "" {
		t.Errorf("MemberId: got empty, want non-empty")
	}
	if got, want := body.Name, "Ada Lovelace"; got != want {
		t.Errorf("Name: got %q, want %q", got, want)
	}
	if got, want := body.Email, "ada.lovelace@example.com"; got != want {
		t.Errorf("Email: got %q, want %q", got, want)
	}
	if got, want := body.Tier, "STANDARD"; got != want {
		t.Errorf("Tier: got %q, want %q", got, want)
	}
	if got, want := body.Status, "ACTIVE"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

func TestRegisterMember_InvalidEmailReturns400InvalidMember(t *testing.T) {
	router, _ := buildRouter(t)
	req := validRegisterBody()
	req.Email = "not-an-email"

	rec := send(router, jsonRequest(t, http.MethodPost, "/members", req))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_member"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestRegisterMember_DuplicateEmailReturns409DuplicateEmail(t *testing.T) {
	router, facade := buildRouter(t)
	seedMember(t, facade, "ada.lovelace@example.com")

	rec := send(router, jsonRequest(t, http.MethodPost, "/members", validRegisterBody()))

	assertStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Error, "duplicate_email"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestRegisterMember_UnknownFieldReturns400(t *testing.T) {
	router, _ := buildRouter(t)
	body := `{"name":"Ada","email":"ada@example.com","extra":"surprise"}`

	rec := send(router, rawJSONRequest(http.MethodPost, "/members", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_member"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// GET /members/{id}
// -----------------------------------------------------------------------------

func TestFindMember_KnownIdReturns200(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/members/"+string(seeded.MemberId), nil))

	assertStatus(t, rec, http.StatusOK)
	body := decodeMemberResponse(t, rec)
	if got, want := body.MemberId, string(seeded.MemberId); got != want {
		t.Errorf("MemberId: got %q, want %q", got, want)
	}
}

func TestFindMember_UnknownIdReturns404MemberNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodGet, "/members/does-not-exist", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "member_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// PATCH /members/{id}/suspend + /reactivate
// -----------------------------------------------------------------------------

func TestSuspend_Returns200WithSuspendedStatus(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/members/"+string(seeded.MemberId)+"/suspend", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeMemberResponse(t, rec)
	if got, want := resp.Status, "SUSPENDED"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

func TestSuspend_UnknownIdReturns404MemberNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/members/does-not-exist/suspend", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "member_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestReactivate_Returns200WithActiveStatus(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")
	if _, err := facade.Suspend(context.Background(), seeded.MemberId); err != nil {
		t.Fatalf("arrange Suspend: %v", err)
	}

	rec := send(router, httptest.NewRequest(http.MethodPatch, "/members/"+string(seeded.MemberId)+"/reactivate", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeMemberResponse(t, rec)
	if got, want := resp.Status, "ACTIVE"; got != want {
		t.Errorf("Status: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// PATCH /members/{id}/tier
// -----------------------------------------------------------------------------

func TestUpgradeTier_PremiumReturns200WithPremiumTier(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")

	body := UpgradeTierRequest{Tier: "PREMIUM"}
	rec := send(router, jsonRequest(t, http.MethodPatch, "/members/"+string(seeded.MemberId)+"/tier", body))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeMemberResponse(t, rec)
	if got, want := resp.Tier, "PREMIUM"; got != want {
		t.Errorf("Tier: got %q, want %q", got, want)
	}
}

func TestUpgradeTier_UnknownIdReturns404MemberNotFound(t *testing.T) {
	router, _ := buildRouter(t)

	body := UpgradeTierRequest{Tier: "PREMIUM"}
	rec := send(router, jsonRequest(t, http.MethodPatch, "/members/does-not-exist/tier", body))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "member_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// GET /members/{id}/eligibility
// -----------------------------------------------------------------------------

func TestCheckEligibility_ActiveMemberReturns200WithEligibleTrue(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/members/"+string(seeded.MemberId)+"/eligibility", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeEligibilityResponse(t, rec)
	if !resp.Eligible {
		t.Errorf("Eligible: got false, want true")
	}
	if resp.MemberId != string(seeded.MemberId) {
		t.Errorf("MemberId: got %q, want %q", resp.MemberId, seeded.MemberId)
	}
	if resp.Reason != "" {
		t.Errorf("Reason: got %q, want empty (omitempty)", resp.Reason)
	}
}

func TestCheckEligibility_SuspendedMemberReturnsEligibleFalseReasonSuspended(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")
	if _, err := facade.Suspend(context.Background(), seeded.MemberId); err != nil {
		t.Fatalf("arrange Suspend: %v", err)
	}

	rec := send(router, httptest.NewRequest(http.MethodGet, "/members/"+string(seeded.MemberId)+"/eligibility", nil))

	assertStatus(t, rec, http.StatusOK)
	resp := decodeEligibilityResponse(t, rec)
	if resp.Eligible {
		t.Errorf("Eligible: got true, want false")
	}
	if resp.Reason != "SUSPENDED" {
		t.Errorf("Reason: got %q, want %q", resp.Reason, "SUSPENDED")
	}
}

func TestCheckEligibility_ActiveMemberOmitsReasonField(t *testing.T) {
	router, facade := buildRouter(t)
	seeded := seedMember(t, facade, "ada@example.com")

	rec := send(router, httptest.NewRequest(http.MethodGet, "/members/"+string(seeded.MemberId)+"/eligibility", nil))

	assertStatus(t, rec, http.StatusOK)
	// The active path must NOT carry a `"reason"` key — omitempty drops the
	// zero string. Assert against the raw body so an accidental
	// `"reason": ""` is caught.
	if bytes.Contains(rec.Body.Bytes(), []byte(`"reason"`)) {
		t.Errorf("body must not contain `reason` key for active member, got %q", rec.Body.String())
	}
}
