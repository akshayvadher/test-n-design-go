//go:build integration

package crucialpath_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/internal/membership"
	"github.com/akshayvadher/test-n-design-go/test/support"
)

// TestSagaCrucialPath boots the full app against real Postgres + Redis and
// walks the auto-loan saga chain end-to-end through the HTTP surface.
//
// Scenario: Alice borrows the only copy of a book → Bob reserves the same
// book → Alice returns. The AutoLoanOnReturnConsumer (Start()ed by the
// composition root) sees LoanReturned, claims Bob's reservation, calls
// lending.Borrow on his behalf, and publishes AutoLoanOpened. Because the
// in-memory bus is synchronous (Publish blocks until every handler has
// returned), the entire chain completes before PATCH /loans/{id}/return
// returns 200 — so the test can assert state immediately, no polling.
//
// Assertions cover the four observable outcomes the saga produces:
//
//  1. Bob now holds a loan on the same copy (returnedAt is NULL).
//  2. Bob's reservation has fulfilled_at set (the claim tx committed).
//  3. The copy is UNAVAILABLE again (lending.Borrow's post-commit
//     catalog.MarkCopyUnavailable ran).
//  4. AutoLoanOpened landed on the bus carrying Bob's reservation id.
func TestSagaCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateSagaTables(t, app)
	})

	t.Run("PATCH /loans/{id}/return auto-loans the head-of-queue reserver", func(t *testing.T) {
		aliceId := registerMemberForSaga(t, app, "Alice Saga", "alice-saga@example.com")
		bobId := registerMemberForSaga(t, app, "Bob Saga", "bob-saga@example.com")
		book, copy := seedBookWithCopy(t, app, "978-0201633610")

		aliceLoan := borrowCopy(t, app, aliceId, copy.CopyId)
		bobReservation := reserveBook(t, app, bobId, book.BookId)

		autoLoanOpened := subscribeForEvent(t, app.Bus, "AutoLoanOpened")
		loanOpened := subscribeForEvent(t, app.Bus, "LoanOpened")
		reservationFulfilled := subscribeForEvent(t, app.Bus, "ReservationFulfilled")

		returnLoan(t, app, aliceLoan.LoanId)

		assertBobGotAutoLoan(t, app, bobId, copy.CopyId)
		assertReservationFulfilled(t, app, bobReservation.ReservationId)
		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusUnavailable)
		assertAutoLoanOpenedEvent(t, autoLoanOpened, book.BookId, bobId, bobReservation.ReservationId)
		assertReservationFulfilledEvent(t, reservationFulfilled, bobReservation.ReservationId)
		assertLoanOpenedForMember(t, loanOpened, bobId)
	})

	t.Run("PATCH /loans/{id}/return with no reservations queued does not auto-loan", func(t *testing.T) {
		truncateSagaTables(t, app)

		aliceId := registerMemberForSaga(t, app, "Alice Solo", "alice-solo@example.com")
		_, copy := seedBookWithCopy(t, app, "978-0596007126")

		aliceLoan := borrowCopy(t, app, aliceId, copy.CopyId)

		autoLoanOpened := subscribeForEvent(t, app.Bus, "AutoLoanOpened")

		returnLoan(t, app, aliceLoan.LoanId)

		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusAvailable)
		assertNoAutoLoanOpened(t, autoLoanOpened)
		assertNoLoanForMember(t, app, aliceId, copy.CopyId, true)
	})

	t.Run("eligibility cascade — suspended reserver is skipped, eligible reserver gets the auto-loan", func(t *testing.T) {
		truncateSagaTables(t, app)

		aliceId := registerMemberForSaga(t, app, "Alice Cascade", "alice-cascade@example.com")
		suspendedId := registerMemberForSaga(t, app, "Suspended Bob", "suspended-bob@example.com")
		carolId := registerMemberForSaga(t, app, "Eligible Carol", "carol-cascade@example.com")
		book, copy := seedBookWithCopy(t, app, "978-0132350884")

		aliceLoan := borrowCopy(t, app, aliceId, copy.CopyId)
		suspendedReservation := reserveBook(t, app, suspendedId, book.BookId)
		carolReservation := reserveBook(t, app, carolId, book.BookId)

		suspendMember(t, app, suspendedId)

		autoLoanOpened := subscribeForEvent(t, app.Bus, "AutoLoanOpened")

		returnLoan(t, app, aliceLoan.LoanId)

		assertBobGotAutoLoan(t, app, carolId, copy.CopyId)
		assertReservationStillPending(t, app, suspendedReservation.ReservationId)
		assertReservationFulfilled(t, app, carolReservation.ReservationId)
		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusUnavailable)
		assertAutoLoanOpenedEvent(t, autoLoanOpened, book.BookId, carolId, carolReservation.ReservationId)
	})

	t.Run("auto-loaned reserver can return the copy with no further auto-loan", func(t *testing.T) {
		truncateSagaTables(t, app)

		aliceId := registerMemberForSaga(t, app, "Alice Chain", "alice-chain@example.com")
		bobId := registerMemberForSaga(t, app, "Bob Chain", "bob-chain@example.com")
		_, copy := seedBookWithCopy(t, app, "978-0321125217")

		aliceLoan := borrowCopy(t, app, aliceId, copy.CopyId)
		_ = reserveBook(t, app, bobId, copy.BookId)

		returnLoan(t, app, aliceLoan.LoanId)

		bobLoanId := findActiveLoanId(t, app, bobId, copy.CopyId)
		if bobLoanId == "" {
			t.Fatal("expected Bob to hold an active loan on the copy after Alice's return")
		}

		autoLoanOpened := subscribeForEvent(t, app.Bus, "AutoLoanOpened")
		returnLoan(t, app, bobLoanId)

		assertCopyStatusViaFacade(t, app, catalog.CopyId(copy.CopyId), catalog.CopyStatusAvailable)
		assertNoAutoLoanOpened(t, autoLoanOpened)
	})
}

// -----------------------------------------------------------------------------
// Saga-specific HTTP helpers
// -----------------------------------------------------------------------------

// registerMemberForSaga registers a member via POST /members and returns the
// minted memberId. Mirrors registerMemberForLending but lives here so the
// saga test reads top-to-bottom without jumping to the lending test file.
func registerMemberForSaga(t *testing.T, app support.BootedApp, name, email string) string {
	t.Helper()
	resp := postJSON(t, app.BaseURL+"/members", registerMemberRequest{Name: name, Email: email})
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /members status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
	}
	var registered memberResponse
	decodeJSON(t, resp.Body, &registered)
	return registered.MemberId
}

// borrowCopy issues POST /loans on behalf of memberId for copyId and returns
// the parsed loan response. Fails the test on a non-201 status.
func borrowCopy(t *testing.T, app support.BootedApp, memberId, copyId string) loanResponse {
	t.Helper()
	resp := postJSON(t, app.BaseURL+"/loans", borrowRequest{MemberId: memberId, CopyId: copyId})
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /loans status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
	}
	var loan loanResponse
	decodeJSON(t, resp.Body, &loan)
	return loan
}

// reserveBook issues POST /reservations for memberId + bookId and returns
// the parsed reservation response.
func reserveBook(t *testing.T, app support.BootedApp, memberId, bookId string) reservationResponse {
	t.Helper()
	resp := postJSON(t, app.BaseURL+"/reservations", reserveRequest{MemberId: memberId, BookId: bookId})
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusCreated; got != want {
		t.Fatalf("POST /reservations status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
	}
	var queued reservationResponse
	decodeJSON(t, resp.Body, &queued)
	return queued
}

// returnLoan issues PATCH /loans/{id}/return and asserts a 200.
func returnLoan(t *testing.T, app support.BootedApp, loanId string) {
	t.Helper()
	resp := patch(t, app.BaseURL+"/loans/"+loanId+"/return")
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("PATCH /loans/%s/return status: got %d, want %d (body=%s)", loanId, got, want, readAll(t, resp.Body))
	}
}

// suspendMember issues PATCH /members/{id}/suspend and asserts a 200.
func suspendMember(t *testing.T, app support.BootedApp, memberId string) {
	t.Helper()
	resp := patch(t, app.BaseURL+"/members/"+memberId+"/suspend")
	defer resp.Body.Close()
	if got, want := resp.StatusCode, http.StatusOK; got != want {
		t.Fatalf("PATCH /members/%s/suspend status: got %d, want %d (body=%s)", memberId, got, want, readAll(t, resp.Body))
	}
}

// -----------------------------------------------------------------------------
// Saga-specific assertions
// -----------------------------------------------------------------------------

// assertBobGotAutoLoan asserts the wired LendingFacade lists exactly one
// active loan for memberId on copyId. Goes through the facade rather than
// raw SQL so the assertion exercises the same read path the saga consumer
// uses inside its claim flow.
func assertBobGotAutoLoan(t *testing.T, app support.BootedApp, memberId, copyId string) {
	t.Helper()
	loans, err := app.LendingFacade.ListLoansFor(context.Background(), membership.MemberId(memberId))
	if err != nil {
		t.Fatalf("ListLoansFor(%q): %v", memberId, err)
	}
	matched := 0
	for _, loan := range loans {
		if string(loan.CopyId) != copyId {
			continue
		}
		if loan.ReturnedAt != nil {
			t.Errorf("loan %q for member %q on copy %q: returnedAt got %v, want nil", loan.LoanId, memberId, copyId, *loan.ReturnedAt)
			continue
		}
		matched++
	}
	if matched != 1 {
		t.Errorf("active loans for member %q on copy %q: got %d, want 1 (loans=%+v)", memberId, copyId, matched, loans)
	}
}

// assertReservationFulfilled asserts the reservations row's fulfilled_at
// column is non-NULL — i.e. the claim tx committed and persisted.
func assertReservationFulfilled(t *testing.T, app support.BootedApp, reservationId string) {
	t.Helper()
	var nonNull bool
	row := app.DB.QueryRowContext(context.Background(), "SELECT fulfilled_at IS NOT NULL FROM reservations WHERE reservation_id = ?", reservationId)
	if err := row.Scan(&nonNull); err != nil {
		t.Fatalf("read fulfilled_at WHERE reservation_id=%q: %v", reservationId, err)
	}
	if !nonNull {
		t.Errorf("reservations.fulfilled_at for reservationId=%q: got NULL, want non-NULL", reservationId)
	}
}

// assertReservationStillPending asserts the reservations row's fulfilled_at
// column is NULL — the consumer skipped this reservation (e.g. the reserver
// was ineligible) and never claimed it.
func assertReservationStillPending(t *testing.T, app support.BootedApp, reservationId string) {
	t.Helper()
	var isNull bool
	row := app.DB.QueryRowContext(context.Background(), "SELECT fulfilled_at IS NULL FROM reservations WHERE reservation_id = ?", reservationId)
	if err := row.Scan(&isNull); err != nil {
		t.Fatalf("read fulfilled_at WHERE reservation_id=%q: %v", reservationId, err)
	}
	if !isNull {
		t.Errorf("reservations.fulfilled_at for reservationId=%q: got non-NULL, want NULL (consumer should have skipped this reservation)", reservationId)
	}
}

// assertAutoLoanOpenedEvent asserts exactly one AutoLoanOpened event landed
// on the bus carrying the expected book + member + reservation ids. The
// in-memory bus is synchronous so by the time PATCH /loans/{id}/return
// returns 200 the event is already in `captured` — a short polling deadline
// covers any scheduler hiccup.
func assertAutoLoanOpenedEvent(t *testing.T, captured *capturedEvents, bookId, memberId, reservationId string) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, evt := range captured.snapshot() {
			opened, ok := evt.(lending.AutoLoanOpened)
			if !ok {
				continue
			}
			if string(opened.BookId) != bookId {
				continue
			}
			if string(opened.MemberId) != memberId {
				continue
			}
			if string(opened.ReservationId) != reservationId {
				continue
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("AutoLoanOpened with bookId=%q memberId=%q reservationId=%q did not arrive on the bus within 100ms (captured=%+v)",
		bookId, memberId, reservationId, captured.snapshot())
}

// assertReservationFulfilledEvent asserts a ReservationFulfilled event for
// reservationId landed on the bus. Documents the staged-event publication
// from the claim tx commit.
func assertReservationFulfilledEvent(t *testing.T, captured *capturedEvents, reservationId string) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, evt := range captured.snapshot() {
			fulfilled, ok := evt.(lending.ReservationFulfilled)
			if !ok {
				continue
			}
			if string(fulfilled.ReservationId) == reservationId {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("ReservationFulfilled for reservationId=%q did not arrive on the bus within 100ms (captured=%+v)",
		reservationId, captured.snapshot())
}

// assertLoanOpenedForMember asserts a LoanOpened event for memberId landed
// on the bus — the auto-loan's borrow ran and emitted the standard
// post-commit LoanOpened.
func assertLoanOpenedForMember(t *testing.T, captured *capturedEvents, memberId string) {
	t.Helper()
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, evt := range captured.snapshot() {
			opened, ok := evt.(lending.LoanOpened)
			if !ok {
				continue
			}
			if string(opened.MemberId) == memberId {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("LoanOpened for memberId=%q did not arrive on the bus within 100ms (captured=%+v)",
		memberId, captured.snapshot())
}

// assertNoAutoLoanOpened asserts the captured slice contains no
// AutoLoanOpened event. Used by the "no reservations queued" and
// "vanilla return" scenarios.
func assertNoAutoLoanOpened(t *testing.T, captured *capturedEvents) {
	t.Helper()
	for _, evt := range captured.snapshot() {
		if _, ok := evt.(lending.AutoLoanOpened); ok {
			t.Errorf("AutoLoanOpened was published when none was expected (captured=%+v)", captured.snapshot())
			return
		}
	}
}

// assertNoLoanForMember asserts memberId holds no active loan on copyId
// (when active=true) — used by the "vanilla return" scenario to prove the
// returning member didn't accidentally get auto-looped.
func assertNoLoanForMember(t *testing.T, app support.BootedApp, memberId, copyId string, active bool) {
	t.Helper()
	loans, err := app.LendingFacade.ListLoansFor(context.Background(), membership.MemberId(memberId))
	if err != nil {
		t.Fatalf("ListLoansFor(%q): %v", memberId, err)
	}
	for _, loan := range loans {
		if string(loan.CopyId) != copyId {
			continue
		}
		if active && loan.ReturnedAt == nil {
			t.Errorf("member %q unexpectedly holds active loan %q on copy %q", memberId, loan.LoanId, copyId)
		}
	}
}

// findActiveLoanId returns the loanId of memberId's currently-active loan
// on copyId, or "" when none exists. Used by the multi-step chain scenario
// to fetch Bob's auto-loan id without depending on a list-loans HTTP route.
func findActiveLoanId(t *testing.T, app support.BootedApp, memberId, copyId string) string {
	t.Helper()
	loans, err := app.LendingFacade.ListLoansFor(context.Background(), membership.MemberId(memberId))
	if err != nil {
		t.Fatalf("ListLoansFor(%q): %v", memberId, err)
	}
	for _, loan := range loans {
		if string(loan.CopyId) != copyId {
			continue
		}
		if loan.ReturnedAt == nil {
			return string(loan.LoanId)
		}
	}
	return ""
}

// truncateSagaTables clears every table the saga crucial-path test touches
// between scenarios. CASCADE matches the lending + fines truncate style so
// foreign-key-free child rows (loans, reservations) clear with their
// parents (copies, books, members).
func truncateSagaTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(),
		"TRUNCATE loans, reservations, fines, categories, copies, books, members RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate saga tables: %v", err)
	}
}
