// handlers_test.go is the HTTP-level spec for the fines module. The
// tests construct a real *fines.Facade with the default in-memory
// substrates (real *lending.Facade + *membership.Facade + *catalog.Facade
// underneath), wrap it in NewHandlers, mount the routes on a chi.NewRouter
// with the DomainErrorMiddleware wired in, and exercise each endpoint
// via httptest.NewRecorder + a hand-built *http.Request.
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
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

var fixedNow = time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)

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

type mutableClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *mutableClock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutableClock) set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

type testScene struct {
	router     chi.Router
	fines      *fines.Facade
	lending    *lending.Facade
	membership *membership.Facade
	catalog    *catalog.Facade
	clock      *mutableClock
}

func buildScene(t *testing.T) *testScene {
	t.Helper()
	logger := silentLogger()
	clock := &mutableClock{now: fixedNow}

	catalogFacade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		NewID:  sequentialIds("cat"),
		Logger: logger,
	})
	membershipFacade := membership.NewFacadeWithOverrides(membership.Overrides{
		NewID:  sequentialIds("mem"),
		Logger: logger,
	})
	lendingFacade := lending.NewFacadeWithOverrides(lending.Overrides{
		Catalog:    catalogFacade,
		Membership: membershipFacade,
		NewID:      sequentialIds("loan"),
		Clock:      clock.read,
		Logger:     logger,
	})
	finesFacade := fines.NewFacadeWithOverrides(fines.Overrides{
		Lending:    lendingFacade,
		Membership: membershipFacade,
		NewID:      sequentialIds("fine"),
		Clock:      clock.read,
		Logger:     logger,
	})

	registry := buildTestRegistry()
	r := chi.NewRouter()
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(r, Deps{Facade: finesFacade, Logger: logger, Clock: clock.read})

	return &testScene{
		router:     r,
		fines:      finesFacade,
		lending:    lendingFacade,
		membership: membershipFacade,
		catalog:    catalogFacade,
		clock:      clock,
	}
}

func buildTestRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&membership.MemberNotFoundError{}, http.StatusNotFound, "member_not_found")
	registry.Register(&fines.FineNotFoundError{}, http.StatusNotFound, "fine_not_found")
	registry.Register(&fines.FineAlreadyPaidError{}, http.StatusConflict, "fine_already_paid")
	registry.Register(&fines.InvalidFineError{}, http.StatusBadRequest, "invalid_fine")
	return registry
}

func send(r chi.Router, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func newRequest(method, target string) *http.Request {
	return httptest.NewRequest(method, target, nil)
}

func decodeBody(t *testing.T, body *bytes.Buffer, dst any) {
	t.Helper()
	if err := json.NewDecoder(body).Decode(dst); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

type errorBody struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func seedAvailableCopy(t *testing.T, s *testScene, seq int) catalog.CopyDto {
	t.Helper()
	isbn := catalog.Isbn("978-" + padLeft(strconv.Itoa(seq), 10, '0'))
	book, err := s.catalog.AddBook(context.Background(), catalog.SampleNewBook(catalog.WithIsbn(isbn)))
	if err != nil {
		t.Fatalf("AddBook: %v", err)
	}
	copyDto, err := s.catalog.RegisterCopy(context.Background(), book.BookId, catalog.SampleNewCopy(catalog.WithBookId(book.BookId)))
	if err != nil {
		t.Fatalf("RegisterCopy: %v", err)
	}
	return copyDto
}

func padLeft(s string, n int, pad byte) string {
	if len(s) >= n {
		return s
	}
	buf := make([]byte, n)
	for i := 0; i < n-len(s); i++ {
		buf[i] = pad
	}
	copy(buf[n-len(s):], s)
	return string(buf)
}

func registerMember(t *testing.T, s *testScene, seq int) membership.MemberDto {
	t.Helper()
	m, err := s.membership.RegisterMember(context.Background(), membership.NewMemberDto{
		Name:  "Member" + strconv.Itoa(seq),
		Email: "fines-http-" + strconv.Itoa(seq) + "@lib.test",
	})
	if err != nil {
		t.Fatalf("RegisterMember: %v", err)
	}
	return m
}

func memberAuth(memberId membership.MemberId) accesscontrol.AuthUser {
	return accesscontrol.AuthUser{MemberID: string(memberId), Role: accesscontrol.RoleMember}
}

// seedOverdueLoanFor opens a loan at fixedNow then advances the scene's
// clock so a subsequent AssessFinesFor call sees the loan as overdue.
func seedOverdueLoanFor(t *testing.T, s *testScene, member membership.MemberDto, copyId catalog.CopyId) lending.LoanDto {
	t.Helper()
	s.clock.set(fixedNow)
	loan, err := s.lending.Borrow(context.Background(), memberAuth(member.MemberId), copyId)
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	s.clock.set(fixedNow.Add(30 * 24 * time.Hour))
	return loan
}

// -----------------------------------------------------------------------------
// AssessFinesFor handler
// -----------------------------------------------------------------------------

func TestHandlers_AssessFinesFor_ReturnsFineList(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)
	copyDto := seedAvailableCopy(t, s, 1)
	seedOverdueLoanFor(t, s, member, copyDto.CopyId)

	rec := send(s.router, newRequest(http.MethodPost, "/members/"+string(member.MemberId)+"/fines/assessments"))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var out []FineResponse
	decodeBody(t, rec.Body, &out)
	if len(out) != 1 {
		t.Errorf("response length: got %d, want 1", len(out))
	}
}

func TestHandlers_AssessFinesFor_EmptyWhenNoOverdueLoans(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)

	rec := send(s.router, newRequest(http.MethodPost, "/members/"+string(member.MemberId)+"/fines/assessments"))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	var out []FineResponse
	decodeBody(t, rec.Body, &out)
	if len(out) != 0 {
		t.Errorf("response length: got %d, want 0", len(out))
	}
}

// -----------------------------------------------------------------------------
// ProcessOverdueLoans handler
// -----------------------------------------------------------------------------

func TestHandlers_ProcessOverdueLoans_Returns204(t *testing.T) {
	s := buildScene(t)
	rec := send(s.router, newRequest(http.MethodPost, "/fines/batch/process"))
	if got, want := rec.Code, http.StatusNoContent; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body: got %q, want empty", rec.Body.String())
	}
}

// -----------------------------------------------------------------------------
// ListFinesFor handler
// -----------------------------------------------------------------------------

func TestHandlers_ListFinesFor_ReturnsAssessed(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)
	copyDto := seedAvailableCopy(t, s, 1)
	seedOverdueLoanFor(t, s, member, copyDto.CopyId)
	if _, err := s.fines.AssessFinesFor(context.Background(), member.MemberId, s.clock.read()); err != nil {
		t.Fatalf("AssessFinesFor seed: %v", err)
	}

	rec := send(s.router, newRequest(http.MethodGet, "/members/"+string(member.MemberId)+"/fines"))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var out []FineResponse
	decodeBody(t, rec.Body, &out)
	if len(out) != 1 {
		t.Errorf("response length: got %d, want 1", len(out))
	}
}

// -----------------------------------------------------------------------------
// FindFine handler
// -----------------------------------------------------------------------------

func TestHandlers_FindFine_Happy(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)
	copyDto := seedAvailableCopy(t, s, 1)
	seedOverdueLoanFor(t, s, member, copyDto.CopyId)
	assessed, err := s.fines.AssessFinesFor(context.Background(), member.MemberId, s.clock.read())
	if err != nil {
		t.Fatalf("AssessFinesFor seed: %v", err)
	}

	rec := send(s.router, newRequest(http.MethodGet, "/fines/"+string(assessed[0].FineId)))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	var out FineResponse
	decodeBody(t, rec.Body, &out)
	if out.FineId != string(assessed[0].FineId) {
		t.Errorf("fineId: got %q, want %q", out.FineId, assessed[0].FineId)
	}
}

func TestHandlers_FindFine_NotFound(t *testing.T) {
	s := buildScene(t)
	rec := send(s.router, newRequest(http.MethodGet, "/fines/missing-fine"))
	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var body errorBody
	decodeBody(t, rec.Body, &body)
	if body.Error != "fine_not_found" {
		t.Errorf("error code: got %q, want %q", body.Error, "fine_not_found")
	}
}

func TestHandlers_FindFine_BlankIdReturns400InvalidFine(t *testing.T) {
	s := buildScene(t)
	rec := send(s.router, newRequest(http.MethodGet, "/fines/%20%20"))
	if got, want := rec.Code, http.StatusBadRequest; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var body errorBody
	decodeBody(t, rec.Body, &body)
	if body.Error != "invalid_fine" {
		t.Errorf("error code: got %q, want %q", body.Error, "invalid_fine")
	}
}

// -----------------------------------------------------------------------------
// PayFine handler
// -----------------------------------------------------------------------------

func TestHandlers_PayFine_HappyReturnsUpdatedFine(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)
	copyDto := seedAvailableCopy(t, s, 1)
	seedOverdueLoanFor(t, s, member, copyDto.CopyId)
	assessed, err := s.fines.AssessFinesFor(context.Background(), member.MemberId, s.clock.read())
	if err != nil {
		t.Fatalf("AssessFinesFor seed: %v", err)
	}

	rec := send(s.router, newRequest(http.MethodPatch, "/fines/"+string(assessed[0].FineId)+"/paid"))
	if got, want := rec.Code, http.StatusOK; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var out FineResponse
	decodeBody(t, rec.Body, &out)
	if out.PaidAt == nil {
		t.Errorf("paidAt: got nil, want populated")
	}
}

func TestHandlers_PayFine_AlreadyPaidReturns409(t *testing.T) {
	s := buildScene(t)
	member := registerMember(t, s, 1)
	copyDto := seedAvailableCopy(t, s, 1)
	seedOverdueLoanFor(t, s, member, copyDto.CopyId)
	assessed, err := s.fines.AssessFinesFor(context.Background(), member.MemberId, s.clock.read())
	if err != nil {
		t.Fatalf("AssessFinesFor seed: %v", err)
	}
	if _, err := s.fines.PayFine(context.Background(), assessed[0].FineId); err != nil {
		t.Fatalf("PayFine seed: %v", err)
	}

	rec := send(s.router, newRequest(http.MethodPatch, "/fines/"+string(assessed[0].FineId)+"/paid"))
	if got, want := rec.Code, http.StatusConflict; got != want {
		t.Fatalf("status: got %d, want %d (body=%s)", got, want, rec.Body.String())
	}
	var body errorBody
	decodeBody(t, rec.Body, &body)
	if body.Error != "fine_already_paid" {
		t.Errorf("error code: got %q, want %q", body.Error, "fine_already_paid")
	}
}

func TestHandlers_PayFine_UnknownReturns404(t *testing.T) {
	s := buildScene(t)
	rec := send(s.router, newRequest(http.MethodPatch, "/fines/missing/paid"))
	if got, want := rec.Code, http.StatusNotFound; got != want {
		t.Fatalf("status: got %d, want %d", got, want)
	}
	var body errorBody
	decodeBody(t, rec.Body, &body)
	if body.Error != "fine_not_found" {
		t.Errorf("error code: got %q, want %q", body.Error, "fine_not_found")
	}
}
