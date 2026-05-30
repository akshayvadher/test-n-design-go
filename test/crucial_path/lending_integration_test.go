//go:build integration

package crucialpath_test

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/shared/events"
	"github.com/akshayvadher/test-n-design-go/test/support"
)

// Wire-shape mirrors for lending HTTP DTOs. The crucial-path test
// re-declares them rather than importing internal/lending/http so a
// silent rename of the public field breaks the integration test — which
// is what we want.

type borrowRequest struct {
	MemberId string `json:"memberId"`
	CopyId   string `json:"copyId"`
}

type reserveRequest struct {
	MemberId string `json:"memberId"`
	BookId   string `json:"bookId"`
}

type loanResponse struct {
	LoanId     string     `json:"loanId"`
	MemberId   string     `json:"memberId"`
	CopyId     string     `json:"copyId"`
	BookId     string     `json:"bookId"`
	BorrowedAt time.Time  `json:"borrowedAt"`
	DueDate    time.Time  `json:"dueDate"`
	ReturnedAt *time.Time `json:"returnedAt,omitempty"`
}

type reservationResponse struct {
	ReservationId string     `json:"reservationId"`
	MemberId      string     `json:"memberId"`
	BookId        string     `json:"bookId"`
	ReservedAt    time.Time  `json:"reservedAt"`
	FulfilledAt   *time.Time `json:"fulfilledAt,omitempty"`
}

// TestLendingCrucialPath boots the full app against real Postgres + Redis
// and walks the lending HTTP surface from seed → borrow → return →
// reserve, asserting status codes, response bodies, row counts, the
// post-commit cross-module catalog state, and that LoanReturned surfaces
// on the bus even though Phase 3 has no consumer subscribed.
func TestLendingCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateLendingTables(t, app)
	})

	memberId := registerMemberForLending(t, app, "Ada Lovelace", "ada-lending@example.com")
	book, copy := seedBookWithCopy(t, app, "978-0135957059")

	var borrowed loanResponse

	t.Run("POST /loans persists a loan, publishes LoanOpened, and flips the copy to UNAVAILABLE", func(t *testing.T) {
		captured := subscribeForEvent(t, app.Bus, "LoanOpened")

		req := borrowRequest{MemberId: memberId, CopyId: copy.CopyId}
		resp := postJSON(t, app.BaseURL+"/loans", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /loans status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		decodeJSON(t, resp.Body, &borrowed)
		if borrowed.LoanId == "" {
			t.Errorf("POST /loans: loanId is empty")
		}
		if borrowed.MemberId != memberId {
			t.Errorf("POST /loans memberId: got %q, want %q", borrowed.MemberId, memberId)
		}
		if borrowed.CopyId != copy.CopyId {
			t.Errorf("POST /loans copyId: got %q, want %q", borrowed.CopyId, copy.CopyId)
		}
		if borrowed.BookId != book.BookId {
			t.Errorf("POST /loans bookId: got %q, want %q", borrowed.BookId, book.BookId)
		}
		if borrowed.BorrowedAt.IsZero() {
			t.Errorf("POST /loans borrowedAt: got zero, want populated")
		}
		if borrowed.DueDate.IsZero() {
			t.Errorf("POST /loans dueDate: got zero, want populated")
		}
		if borrowed.ReturnedAt != nil {
			t.Errorf("POST /loans returnedAt: got %v, want nil for a freshly opened loan", *borrowed.ReturnedAt)
		}

		assertLoansRowCount(t, app, borrowed.LoanId, 1)
		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusUnavailable)
		assertEventArrived(t, captured, borrowed.LoanId)
	})

	t.Run("POST /loans against the same UNAVAILABLE copy returns 409 + copy_unavailable", func(t *testing.T) {
		preCount := loansRowCount(t, app)

		req := borrowRequest{MemberId: memberId, CopyId: copy.CopyId}
		resp := postJSON(t, app.BaseURL+"/loans", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("POST /loans (already unavailable) status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "copy_unavailable" {
			t.Errorf("error: got %q, want %q", errBody.Error, "copy_unavailable")
		}

		if postCount := loansRowCount(t, app); postCount != preCount {
			t.Errorf("loans row count: got %d, want %d (no row should have been inserted)", postCount, preCount)
		}
	})

	t.Run("PATCH /loans/{loanId}/return returns 200, flips the copy back to AVAILABLE, and publishes LoanReturned", func(t *testing.T) {
		captured := subscribeForEvent(t, app.Bus, "LoanReturned")

		resp := patch(t, app.BaseURL+"/loans/"+borrowed.LoanId+"/return")
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /loans/{id}/return status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var returned loanResponse
		decodeJSON(t, resp.Body, &returned)
		if returned.LoanId != borrowed.LoanId {
			t.Errorf("PATCH /loans/{id}/return loanId: got %q, want %q", returned.LoanId, borrowed.LoanId)
		}
		if returned.ReturnedAt == nil {
			t.Fatal("PATCH /loans/{id}/return returnedAt: got nil, want populated")
		}

		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusAvailable)
		assertReturnedAtRow(t, app, returned.LoanId)
		assertEventArrived(t, captured, borrowed.LoanId)
	})

	t.Run("POST /reservations queues a reservation and inserts a reservations row", func(t *testing.T) {
		req := reserveRequest{MemberId: memberId, BookId: book.BookId}
		resp := postJSON(t, app.BaseURL+"/reservations", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusCreated; got != want {
			t.Fatalf("POST /reservations status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var queued reservationResponse
		decodeJSON(t, resp.Body, &queued)
		if queued.ReservationId == "" {
			t.Errorf("POST /reservations: reservationId is empty")
		}
		if queued.MemberId != memberId {
			t.Errorf("POST /reservations memberId: got %q, want %q", queued.MemberId, memberId)
		}
		if queued.BookId != book.BookId {
			t.Errorf("POST /reservations bookId: got %q, want %q", queued.BookId, book.BookId)
		}
		if queued.FulfilledAt != nil {
			t.Errorf("POST /reservations fulfilledAt: got %v, want nil", *queued.FulfilledAt)
		}

		assertReservationsRowCount(t, app, queued.ReservationId, 1)
	})

	t.Run("PATCH /members/{id}/suspend then POST /loans returns 409 + member_ineligible", func(t *testing.T) {
		// Free the copy first so the borrow attempt fails on eligibility,
		// not on copy_unavailable.
		freshBook, freshCopy := seedBookWithCopy(t, app, "978-0134685991")

		suspendResp := patch(t, app.BaseURL+"/members/"+memberId+"/suspend")
		defer suspendResp.Body.Close()
		if got, want := suspendResp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("PATCH /suspend status: got %d, want %d", got, want)
		}

		preCount := loansRowCount(t, app)

		req := borrowRequest{MemberId: memberId, CopyId: freshCopy.CopyId}
		resp := postJSON(t, app.BaseURL+"/loans", req)
		defer resp.Body.Close()

		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("POST /loans (suspended) status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var errBody errorResponse
		decodeJSON(t, resp.Body, &errBody)
		if errBody.Error != "member_ineligible" {
			t.Errorf("error: got %q, want %q", errBody.Error, "member_ineligible")
		}
		if postCount := loansRowCount(t, app); postCount != preCount {
			t.Errorf("loans row count: got %d, want %d (no row should have been inserted)", postCount, preCount)
		}
		_ = freshBook
	})
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// registerMemberForLending registers a member via POST /members and returns
// the minted memberId. Uses the existing test helpers (defined in the
// membership crucial-path test file in the same package).
func registerMemberForLending(t *testing.T, app support.BootedApp, name, email string) string {
	t.Helper()
	req := registerMemberRequest{Name: name, Email: email}
	resp := postJSON(t, app.BaseURL+"/members", req)
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /members status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
	}
	var registered memberResponse
	decodeJSON(t, resp.Body, &registered)
	return registered.MemberId
}

// seedBookWithCopy adds a book + an AVAILABLE copy via the catalog HTTP
// surface and returns both for use in subsequent assertions.
func seedBookWithCopy(t *testing.T, app support.BootedApp, isbn string) (bookResponse, copyResponse) {
	t.Helper()
	bookReq := bookRequest{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    isbn,
	}
	bookResp := postJSON(t, app.BaseURL+"/books", bookReq)
	defer bookResp.Body.Close()
	if got, want := bookResp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /books status: got %d, want %d (body=%s)", got, want, readAll(t, bookResp.Body))
	}
	var book bookResponse
	decodeJSON(t, bookResp.Body, &book)

	copyReq := newCopyRequest{Condition: "GOOD"}
	copyResp := postJSON(t, app.BaseURL+"/books/"+book.BookId+"/copies", copyReq)
	defer copyResp.Body.Close()
	if got, want := copyResp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /copies status: got %d, want %d (body=%s)", got, want, readAll(t, copyResp.Body))
	}
	var copy copyResponse
	decodeJSON(t, copyResp.Body, &copy)
	return book, copy
}

// capturedEvents records events from the wired bus under a mutex so the
// subscription handler is safe to read from the test goroutine.
type capturedEvents struct {
	mu sync.Mutex
	xs []events.DomainEvent
}

// snapshot returns a defensive copy of the captured events.
func (c *capturedEvents) snapshot() []events.DomainEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.DomainEvent, len(c.xs))
	copy(out, c.xs)
	return out
}

// subscribeForEvent subscribes to eventType on the wired bus and returns
// a *capturedEvents the test reads after the HTTP call. The subscription
// is removed via t.Cleanup so successive tests do not accumulate
// subscribers.
func subscribeForEvent(t *testing.T, bus events.EventBus, eventType string) *capturedEvents {
	t.Helper()
	cap := &capturedEvents{}
	unsubscribe := bus.Subscribe(eventType, func(_ context.Context, evt events.DomainEvent) error {
		cap.mu.Lock()
		cap.xs = append(cap.xs, evt)
		cap.mu.Unlock()
		return nil
	})
	t.Cleanup(unsubscribe)
	return cap
}

// assertEventArrived asserts that exactly one event with a matching
// LoanId surfaced on captured. Tolerates short publish latency by
// polling for up to 100ms (publishes are synchronous in-process so this
// is normally instantaneous; the deadline protects against scheduler
// hiccups under load).
func assertEventArrived(t *testing.T, captured *capturedEvents, loanId string) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		if matchedLoanEvent(captured.snapshot(), loanId) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("event for loanId %q did not arrive on the bus within 100ms (captured=%+v)", loanId, captured.snapshot())
}

// matchedLoanEvent returns true if any of xs is a LoanOpened or
// LoanReturned event whose LoanId matches loanId.
func matchedLoanEvent(xs []events.DomainEvent, loanId string) bool {
	for _, evt := range xs {
		switch e := evt.(type) {
		case lending.LoanOpened:
			if string(e.LoanId) == loanId {
				return true
			}
		case lending.LoanReturned:
			if string(e.LoanId) == loanId {
				return true
			}
		}
	}
	return false
}

// assertLoansRowCount runs `SELECT COUNT(*) FROM loans WHERE loan_id = ?`
// against the wired *bun.DB and asserts the result equals want.
func assertLoansRowCount(t *testing.T, app support.BootedApp, loanId string, want int) {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM loans WHERE loan_id = ?", loanId)
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count loans WHERE loan_id=%q: %v", loanId, err)
	}
	if got != want {
		t.Errorf("loans row count for loanId=%q: got %d, want %d", loanId, got, want)
	}
}

// loansRowCount returns the total number of rows in `loans`. Used by
// negative-path assertions that want to prove no insert happened.
func loansRowCount(t *testing.T, app support.BootedApp) int {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM loans")
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count loans: %v", err)
	}
	return got
}

// assertReservationsRowCount runs
// `SELECT COUNT(*) FROM reservations WHERE reservation_id = ?` and
// asserts the result.
func assertReservationsRowCount(t *testing.T, app support.BootedApp, reservationId string, want int) {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM reservations WHERE reservation_id = ?", reservationId)
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count reservations WHERE reservation_id=%q: %v", reservationId, err)
	}
	if got != want {
		t.Errorf("reservations row count for reservationId=%q: got %d, want %d", reservationId, got, want)
	}
}

// assertReturnedAtRow asserts the loans row's returned_at column is
// non-NULL after ReturnLoan.
func assertReturnedAtRow(t *testing.T, app support.BootedApp, loanId string) {
	t.Helper()
	var nonNull bool
	row := app.DB.QueryRowContext(context.Background(), "SELECT returned_at IS NOT NULL FROM loans WHERE loan_id = ?", loanId)
	if err := row.Scan(&nonNull); err != nil {
		t.Fatalf("read returned_at WHERE loan_id=%q: %v", loanId, err)
	}
	if !nonNull {
		t.Errorf("loans.returned_at for loanId=%q: got NULL, want non-NULL", loanId)
	}
}

// assertCopyStatusViaFacade calls the wired CatalogFacade directly and
// asserts the copy's status. Documents the post-commit cross-module
// mutation (Borrow flips to UNAVAILABLE; ReturnLoan flips back to
// AVAILABLE).
func assertCopyStatusViaFacade(t *testing.T, app support.BootedApp, copyId catalog.CopyId, want catalog.CopyStatus) {
	t.Helper()
	got, err := app.CatalogFacade.FindCopy(context.Background(), copyId)
	if err != nil {
		t.Fatalf("CatalogFacade.FindCopy(%q): %v", copyId, err)
	}
	if got.Status != want {
		t.Errorf("copy %q status: got %q, want %q", copyId, got.Status, want)
	}
}

// truncateLendingTables clears the loans + reservations tables so
// subsequent crucial-path tests run on a clean substrate.
func truncateLendingTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(), "TRUNCATE loans, reservations RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate lending tables: %v", err)
	}
}
