// handlers_test.go is Slice 7's HTTP-level spec for the lending module.
// The tests construct a real *lending.Facade with the default in-memory
// substrates (no double substitution), wrap it in NewHandlers, mount the
// routes on a chi.NewRouter with the DomainErrorMiddleware wired in, and
// exercise each endpoint via httptest.NewRecorder + a hand-built
// *http.Request.
//
// No mocks, no testify — stdlib testing only. Assertions use json.Decoder
// against the response body so whitespace is tolerated and the assertions
// remain field-level rather than byte-level.
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

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
	sharedhttp "github.com/akshayvadher/test-n-design-go/internal/shared/http"
)

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// sequentialIds returns a deterministic id generator so minted ids are
// predictable in assertions.
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

// testScene bundles the wired router with the cross-module facades so each
// test can seed a book + copy + member directly via the shared in-memory
// substrates the lending facade is wired against.
type testScene struct {
	router     chi.Router
	catalog    *catalog.Facade
	membership *membership.Facade
	lending    *lending.Facade
}

// buildScene constructs a fresh lending facade wired to fresh in-memory
// catalog + membership facades + the in-memory tx substrate, registers the
// lending routes on a chi router with the production domain-error registry
// pre-loaded, and returns the bundle for the test to drive.
func buildScene(t *testing.T) *testScene {
	t.Helper()
	logger := silentLogger()

	catalogFacade := catalog.NewFacadeWithOverrides(catalog.Overrides{
		NewID:  sequentialIds("cat"),
		Logger: logger,
	})
	membershipFacade := membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{
		NewID:  sequentialIds("mem"),
		Logger: logger,
	})
	lendingFacade := lending.NewFacadeWithOverrides(lending.Overrides{
		Catalog:    catalogFacade,
		Membership: membershipFacade,
		NewID:      sequentialIds("loan"),
		Logger:     logger,
	})

	registry := buildTestRegistry()
	r := chi.NewRouter()
	r.Use(sharedhttp.DomainErrorMiddleware(registry, logger))
	Wire(r, Deps{Facade: lendingFacade, Logger: logger})

	return &testScene{
		router:     r,
		catalog:    catalogFacade,
		membership: membershipFacade,
		lending:    lendingFacade,
	}
}

// buildTestRegistry mirrors internal/app/wiring.go's full registration
// block so the HTTP tests verify the same mapping production uses.
func buildTestRegistry() *sharedhttp.DomainErrorRegistry {
	registry := sharedhttp.NewDomainErrorRegistry()
	registry.Register(&accesscontrol.UnauthorizedRoleError{}, http.StatusForbidden, "unauthorized_role")
	registry.Register(&accesscontrol.UnknownActionError{}, http.StatusForbidden, "unknown_action")
	registry.Register(&catalog.InvalidBookError{}, http.StatusBadRequest, "invalid_book")
	registry.Register(&catalog.InvalidCopyError{}, http.StatusBadRequest, "invalid_copy")
	registry.Register(&catalog.BookNotFoundError{}, http.StatusNotFound, "book_not_found")
	registry.Register(&catalog.CopyNotFoundError{}, http.StatusNotFound, "copy_not_found")
	registry.Register(&catalog.DuplicateIsbnError{}, http.StatusConflict, "duplicate_isbn")
	registry.Register(&membership.InvalidMemberError{}, http.StatusBadRequest, "invalid_member")
	registry.Register(&membership.MemberNotFoundError{}, http.StatusNotFound, "member_not_found")
	registry.Register(&membership.DuplicateEmailError{}, http.StatusConflict, "duplicate_email")
	registry.Register(&lending.LoanNotFoundError{}, http.StatusNotFound, "loan_not_found")
	registry.Register(&lending.ReservationNotFoundError{}, http.StatusNotFound, "reservation_not_found")
	registry.Register(&lending.CopyUnavailableError{}, http.StatusConflict, "copy_unavailable")
	registry.Register(&lending.MemberIneligibleError{}, http.StatusConflict, "member_ineligible")
	registry.Register(&lending.BorrowValidationError{}, http.StatusBadRequest, "invalid_borrow")
	registry.Register(&lending.ReserveValidationError{}, http.StatusBadRequest, "invalid_reserve")
	registry.Register(&lending.ReturnLoanValidationError{}, http.StatusBadRequest, "invalid_return")
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

// rawJSONRequest builds an *http.Request with body as raw bytes — for
// tests that need a body the json package cannot easily express.
func rawJSONRequest(method, target, body string) *http.Request {
	req := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// decodeLoanResponse decodes the recorder's body into a LoanResponse.
func decodeLoanResponse(t *testing.T, rec *httptest.ResponseRecorder) LoanResponse {
	t.Helper()
	var resp LoanResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode LoanResponse: %v (body=%q)", err, rec.Body.String())
	}
	return resp
}

// decodeReservationResponse decodes the recorder's body into a
// ReservationResponse.
func decodeReservationResponse(t *testing.T, rec *httptest.ResponseRecorder) ReservationResponse {
	t.Helper()
	var resp ReservationResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode ReservationResponse: %v (body=%q)", err, rec.Body.String())
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

// seedMember registers a member via the underlying membership facade and
// returns the created DTO so downstream assertions can use the minted id.
func seedMember(t *testing.T, scene *testScene, name, email string) membership.MemberDto {
	t.Helper()
	member, err := scene.membership.RegisterMember(context.Background(), membership.NewMemberDto{
		Name:  name,
		Email: email,
	})
	if err != nil {
		t.Fatalf("seed member: %v", err)
	}
	return member
}

// seedBookAndCopy seeds a book + an AVAILABLE copy via the underlying
// catalog facade and returns both for use in subsequent assertions.
func seedBookAndCopy(t *testing.T, scene *testScene, isbn string) (catalog.BookDto, catalog.CopyDto) {
	t.Helper()
	book, err := scene.catalog.AddBook(context.Background(), catalog.NewBookDto{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    catalog.Isbn(isbn),
	})
	if err != nil {
		t.Fatalf("seed book: %v", err)
	}
	copy, err := scene.catalog.RegisterCopy(context.Background(), book.BookId, catalog.NewCopyDto{
		BookId:    book.BookId,
		Condition: catalog.CopyConditionGood,
	})
	if err != nil {
		t.Fatalf("seed copy: %v", err)
	}
	return book, copy
}

// suspendMember flips a member to SUSPENDED via the underlying membership
// facade.
func suspendMember(t *testing.T, scene *testScene, memberId membership.MemberId) {
	t.Helper()
	if _, err := scene.membership.Suspend(context.Background(), memberId); err != nil {
		t.Fatalf("suspend member: %v", err)
	}
}

// markCopyUnavailable flips a copy to UNAVAILABLE via the underlying
// catalog facade.
func markCopyUnavailable(t *testing.T, scene *testScene, copyId catalog.CopyId) {
	t.Helper()
	if _, err := scene.catalog.MarkCopyUnavailable(context.Background(), copyId); err != nil {
		t.Fatalf("mark copy unavailable: %v", err)
	}
}

// -----------------------------------------------------------------------------
// POST /loans
// -----------------------------------------------------------------------------

func TestBorrow_ValidBodyReturns201AndLoanResponse(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	book, copy := seedBookAndCopy(t, scene, "978-0135957059")

	body := BorrowRequest{MemberId: string(member.MemberId), CopyId: string(copy.CopyId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusCreated)
	resp := decodeLoanResponse(t, rec)
	if resp.LoanId == "" {
		t.Errorf("LoanId: got empty, want non-empty")
	}
	if got, want := resp.MemberId, string(member.MemberId); got != want {
		t.Errorf("MemberId: got %q, want %q", got, want)
	}
	if got, want := resp.CopyId, string(copy.CopyId); got != want {
		t.Errorf("CopyId: got %q, want %q", got, want)
	}
	if got, want := resp.BookId, string(book.BookId); got != want {
		t.Errorf("BookId: got %q, want %q", got, want)
	}
	if resp.BorrowedAt.IsZero() {
		t.Errorf("BorrowedAt: got zero, want populated")
	}
	if resp.DueDate.IsZero() {
		t.Errorf("DueDate: got zero, want populated")
	}
	if resp.ReturnedAt != nil {
		t.Errorf("ReturnedAt: got %v, want nil for a freshly opened loan", *resp.ReturnedAt)
	}
}

func TestBorrow_SuspendedMemberReturns409MemberIneligible(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	_, copy := seedBookAndCopy(t, scene, "978-0135957059")
	suspendMember(t, scene, member.MemberId)

	body := BorrowRequest{MemberId: string(member.MemberId), CopyId: string(copy.CopyId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Error, "member_ineligible"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestBorrow_UnavailableCopyReturns409CopyUnavailable(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	_, copy := seedBookAndCopy(t, scene, "978-0135957059")
	markCopyUnavailable(t, scene, copy.CopyId)

	body := BorrowRequest{MemberId: string(member.MemberId), CopyId: string(copy.CopyId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Error, "copy_unavailable"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestBorrow_UnknownCopyReturns404CopyNotFound(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")

	body := BorrowRequest{MemberId: string(member.MemberId), CopyId: "does-not-exist"}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "copy_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestBorrow_BlankMemberIdReturns400InvalidBorrow(t *testing.T) {
	scene := buildScene(t)
	_, copy := seedBookAndCopy(t, scene, "978-0135957059")

	body := BorrowRequest{MemberId: "", CopyId: string(copy.CopyId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_borrow"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestBorrow_UnknownFieldReturns400InvalidBorrow(t *testing.T) {
	scene := buildScene(t)

	body := `{"memberId":"m-1","copyId":"c-1","extra":"surprise"}`
	rec := send(scene.router, rawJSONRequest(http.MethodPost, "/loans", body))

	assertStatus(t, rec, http.StatusBadRequest)
	if got, want := decodeError(t, rec).Error, "invalid_borrow"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// PATCH /loans/{loanId}/return
// -----------------------------------------------------------------------------

func TestReturnLoan_KnownLoanReturns200AndPopulatedReturnedAt(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	_, copy := seedBookAndCopy(t, scene, "978-0135957059")

	borrowReq := BorrowRequest{MemberId: string(member.MemberId), CopyId: string(copy.CopyId)}
	borrowRec := send(scene.router, jsonRequest(t, http.MethodPost, "/loans", borrowReq))
	assertStatus(t, borrowRec, http.StatusCreated)
	borrowed := decodeLoanResponse(t, borrowRec)

	returnRec := send(scene.router, httptest.NewRequest(http.MethodPatch, "/loans/"+borrowed.LoanId+"/return", nil))

	assertStatus(t, returnRec, http.StatusOK)
	returned := decodeLoanResponse(t, returnRec)
	if returned.LoanId != borrowed.LoanId {
		t.Errorf("LoanId: got %q, want %q", returned.LoanId, borrowed.LoanId)
	}
	if returned.ReturnedAt == nil {
		t.Fatalf("ReturnedAt: got nil, want populated")
	}
	if returned.ReturnedAt.IsZero() {
		t.Errorf("ReturnedAt: got zero, want non-zero")
	}
}

func TestReturnLoan_UnknownLoanReturns404LoanNotFound(t *testing.T) {
	scene := buildScene(t)

	rec := send(scene.router, httptest.NewRequest(http.MethodPatch, "/loans/does-not-exist/return", nil))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "loan_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

// -----------------------------------------------------------------------------
// POST /reservations
// -----------------------------------------------------------------------------

func TestReserve_ValidBodyReturns201AndReservationResponse(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	book, _ := seedBookAndCopy(t, scene, "978-0135957059")

	body := ReserveRequest{MemberId: string(member.MemberId), BookId: string(book.BookId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/reservations", body))

	assertStatus(t, rec, http.StatusCreated)
	resp := decodeReservationResponse(t, rec)
	if resp.ReservationId == "" {
		t.Errorf("ReservationId: got empty, want non-empty")
	}
	if got, want := resp.MemberId, string(member.MemberId); got != want {
		t.Errorf("MemberId: got %q, want %q", got, want)
	}
	if got, want := resp.BookId, string(book.BookId); got != want {
		t.Errorf("BookId: got %q, want %q", got, want)
	}
	if resp.ReservedAt.IsZero() {
		t.Errorf("ReservedAt: got zero, want populated")
	}
	if resp.FulfilledAt != nil {
		t.Errorf("FulfilledAt: got %v, want nil for a freshly queued reservation", *resp.FulfilledAt)
	}
}

func TestReserve_SuspendedMemberReturns409MemberIneligible(t *testing.T) {
	scene := buildScene(t)
	member := seedMember(t, scene, "Ada", "ada@example.com")
	book, _ := seedBookAndCopy(t, scene, "978-0135957059")
	suspendMember(t, scene, member.MemberId)

	body := ReserveRequest{MemberId: string(member.MemberId), BookId: string(book.BookId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/reservations", body))

	assertStatus(t, rec, http.StatusConflict)
	if got, want := decodeError(t, rec).Error, "member_ineligible"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}

func TestReserve_UnknownMemberReturns404MemberNotFound(t *testing.T) {
	scene := buildScene(t)
	book, _ := seedBookAndCopy(t, scene, "978-0135957059")

	body := ReserveRequest{MemberId: "does-not-exist", BookId: string(book.BookId)}
	rec := send(scene.router, jsonRequest(t, http.MethodPost, "/reservations", body))

	assertStatus(t, rec, http.StatusNotFound)
	if got, want := decodeError(t, rec).Error, "member_not_found"; got != want {
		t.Errorf("error code: got %q, want %q", got, want)
	}
}
