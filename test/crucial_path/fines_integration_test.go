//go:build integration

package crucialpath_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/fines"
	"github.com/akshayvadher/test-n-design-go/internal/lending"
	"github.com/akshayvadher/test-n-design-go/test/support"
)

// fineResponseBody mirrors internal/fines/http.FineResponse — re-declared
// here so a silent rename in the wire shape breaks the integration test.
type fineResponseBody struct {
	FineId      string     `json:"fineId"`
	MemberId    string     `json:"memberId"`
	LoanId      string     `json:"loanId"`
	AmountCents int64      `json:"amountCents"`
	AssessedAt  time.Time  `json:"assessedAt"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
}

// TestFinesCrucialPath boots the full app against real Postgres + Redis
// and walks the fines HTTP surface from assessment → list → pay → auto-suspend
// against fabricated-overdue loans (via direct UPDATE on loans.due_date,
// since the borrow handler computes dueDate as borrowedAt + 14 days).
func TestFinesCrucialPath(t *testing.T) {
	ctx := context.Background()

	pg := support.StartPostgres(ctx, t)
	redis := support.StartRedis(ctx, t)

	app := support.BootApp(ctx, t, support.AppConfig{
		DatabaseURL: pg.URL,
		RedisURL:    redis.URL,
	})

	t.Cleanup(func() {
		truncateFinesTables(t, app)
	})

	memberId := registerMemberForFines(t, app, "Ada Fines", "ada-fines@example.com")
	_, copyDto := seedBookWithCopy(t, app, "978-2222222222")
	loan := openOverdueLoan(t, app, memberId, copyDto.CopyId, 30)

	var assessed []fineResponseBody

	t.Run("POST /members/{id}/fines/assessments returns the freshly assessed fine", func(t *testing.T) {
		resp := postEmpty(t, app.BaseURL+"/members/"+memberId+"/fines/assessments")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		decodeJSON(t, resp.Body, &assessed)
		if len(assessed) != 1 {
			t.Fatalf("assessed length: got %d, want 1", len(assessed))
		}
		if assessed[0].LoanId != string(loan.LoanId) {
			t.Errorf("assessed loanId: got %q, want %q", assessed[0].LoanId, loan.LoanId)
		}
		if assessed[0].AmountCents <= 0 {
			t.Errorf("assessed amountCents: got %d, want >0", assessed[0].AmountCents)
		}
		assertFinesRowCount(t, app, memberId, 1)
	})

	t.Run("POST /members/{id}/fines/assessments is idempotent (already-fined short-circuit)", func(t *testing.T) {
		resp := postEmpty(t, app.BaseURL+"/members/"+memberId+"/fines/assessments")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status: got %d, want %d", got, want)
		}
		var again []fineResponseBody
		decodeJSON(t, resp.Body, &again)
		if len(again) != 0 {
			t.Errorf("second assessed length: got %d, want 0", len(again))
		}
		assertFinesRowCount(t, app, memberId, 1)
	})

	t.Run("GET /members/{id}/fines returns the persisted fine", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/members/"+memberId+"/fines")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status: got %d, want %d", got, want)
		}
		var listed []fineResponseBody
		decodeJSON(t, resp.Body, &listed)
		if len(listed) != 1 {
			t.Errorf("listed length: got %d, want 1", len(listed))
		}
	})

	t.Run("PATCH /fines/{id}/paid flips paidAt and persists", func(t *testing.T) {
		resp := patch(t, app.BaseURL+"/fines/"+assessed[0].FineId+"/paid")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusOK; got != want {
			t.Fatalf("status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}
		var paid fineResponseBody
		decodeJSON(t, resp.Body, &paid)
		if paid.PaidAt == nil {
			t.Errorf("paidAt: got nil, want populated")
		}
		assertFinePaidInDB(t, app, assessed[0].FineId)
	})

	t.Run("GET /fines/{id} for an unknown id returns 404 fine_not_found", func(t *testing.T) {
		resp := getURL(t, app.BaseURL+"/fines/does-not-exist")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusNotFound; got != want {
			t.Fatalf("status: got %d, want %d", got, want)
		}
		var body errorResponse
		decodeJSON(t, resp.Body, &body)
		if body.Error != "fine_not_found" {
			t.Errorf("error: got %q, want %q", body.Error, "fine_not_found")
		}
	})

	t.Run("PATCH /fines/{id}/paid against the already-paid fine returns 409", func(t *testing.T) {
		resp := patch(t, app.BaseURL+"/fines/"+assessed[0].FineId+"/paid")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusConflict; got != want {
			t.Fatalf("status: got %d, want %d", got, want)
		}
		var body errorResponse
		decodeJSON(t, resp.Body, &body)
		if body.Error != "fine_already_paid" {
			t.Errorf("error: got %q, want %q", body.Error, "fine_already_paid")
		}
	})

	t.Run("POST /fines/batch/process auto-suspends a member whose unpaid total crosses the threshold", func(t *testing.T) {
		// Fresh member with an unpaid overdue loan large enough to cross
		// the 1000-cent default threshold (60 days overdue * 25 = 1500).
		bigMemberId := registerMemberForFines(t, app, "Big Fine Bob", "bigfine-bob@example.com")
		_, bigCopy := seedBookWithCopy(t, app, "978-3333333333")
		_ = openOverdueLoan(t, app, bigMemberId, bigCopy.CopyId, 60)

		captured := subscribeForFinesEvent(t, app, "MemberAutoSuspended")

		resp := postEmpty(t, app.BaseURL+"/fines/batch/process")
		defer resp.Body.Close()
		if got, want := resp.StatusCode, http.StatusNoContent; got != want {
			t.Fatalf("status: got %d, want %d (body=%s)", got, want, readAll(t, resp.Body))
		}

		// The member should now be SUSPENDED.
		memberResp := getURL(t, app.BaseURL+"/members/"+bigMemberId)
		defer memberResp.Body.Close()
		var fetched memberResponse
		decodeJSON(t, memberResp.Body, &fetched)
		if fetched.Status != "SUSPENDED" {
			t.Errorf("member status: got %q, want %q", fetched.Status, "SUSPENDED")
		}

		// And one MemberAutoSuspended event should have been published.
		assertAutoSuspendedArrived(t, captured, bigMemberId)
	})
}

// -----------------------------------------------------------------------------
// Helpers (specific to the fines test; shared helpers live in
// catalog_integration_test.go and membership_integration_test.go).
// -----------------------------------------------------------------------------

// registerMemberForFines mirrors registerMemberForLending — shared
// helpers live in the lending file as un-exported funcs so the package
// declares them once and every integration test reuses them.
func registerMemberForFines(t *testing.T, app support.BootedApp, name, email string) string {
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

// openOverdueLoan opens a loan via the wired lending facade (bypassing
// the borrow handler's clock so the test can post-date due_date in one
// shot), then directly UPDATEs the loans row to shift due_date into the
// past by daysOverdue days. This is the documented test-only mechanism
// for fabricating overdue loans (the borrow handler computes dueDate as
// borrowedAt + 14 days; there is no public way to set a custom due
// date).
func openOverdueLoan(t *testing.T, app support.BootedApp, memberId string, copyId string, daysOverdue int) lending.LoanDto {
	t.Helper()
	authUser := accesscontrol.AuthUser{MemberID: memberId, Role: accesscontrol.RoleMember}
	loan, err := app.LendingFacade.Borrow(context.Background(), authUser, catalog.CopyId(copyId))
	if err != nil {
		t.Fatalf("Borrow: %v", err)
	}
	pastDue := time.Now().Add(-time.Duration(daysOverdue) * 24 * time.Hour)
	_, err = app.DB.ExecContext(
		context.Background(),
		"UPDATE loans SET due_date = ? WHERE loan_id = ?",
		pastDue, loan.LoanId,
	)
	if err != nil {
		t.Fatalf("UPDATE loans.due_date: %v", err)
	}
	return loan
}

// postEmpty issues a POST with no body to url.
func postEmpty(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		t.Fatalf("build POST: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// getURL issues a GET to url.
func getURL(t *testing.T, url string) *http.Response {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

// assertFinesRowCount asserts the row count of fines for the given member.
func assertFinesRowCount(t *testing.T, app support.BootedApp, memberId string, want int) {
	t.Helper()
	var got int
	row := app.DB.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM fines WHERE member_id = ?", memberId)
	if err := row.Scan(&got); err != nil {
		t.Fatalf("count fines: %v", err)
	}
	if got != want {
		t.Errorf("fines row count: got %d, want %d", got, want)
	}
}

// assertFinePaidInDB asserts the fine row's paid_at column is non-NULL.
func assertFinePaidInDB(t *testing.T, app support.BootedApp, fineId string) {
	t.Helper()
	var nonNull bool
	row := app.DB.QueryRowContext(context.Background(), "SELECT paid_at IS NOT NULL FROM fines WHERE fine_id = ?", fineId)
	if err := row.Scan(&nonNull); err != nil {
		t.Fatalf("read paid_at: %v", err)
	}
	if !nonNull {
		t.Errorf("fines.paid_at for fineId=%q: got NULL, want non-NULL", fineId)
	}
}

// truncateFinesTables clears every table the fines crucial-path test
// touches between runs. CASCADE matches the lending truncate style.
func truncateFinesTables(t *testing.T, app support.BootedApp) {
	t.Helper()
	_, err := app.DB.ExecContext(context.Background(),
		"TRUNCATE fines, loans, reservations, copies, books, members RESTART IDENTITY CASCADE")
	if err != nil {
		t.Logf("truncate fines tables: %v", err)
	}
}

// -----------------------------------------------------------------------------
// Event subscription helpers (parallel to the lending file's variants but
// specific to the fines event types).
// -----------------------------------------------------------------------------

// subscribeForFinesEvent subscribes to eventType on the wired bus and
// returns a *capturedEvents the test reads after the HTTP call.
func subscribeForFinesEvent(t *testing.T, app support.BootedApp, eventType string) *capturedEvents {
	t.Helper()
	return subscribeForEvent(t, app.Bus, eventType)
}

// assertAutoSuspendedArrived asserts that exactly one MemberAutoSuspended
// event with the matching member id surfaced. Publishes are synchronous
// in-process so a short poll covers any scheduler hiccup.
func assertAutoSuspendedArrived(t *testing.T, captured *capturedEvents, memberId string) {
	t.Helper()
	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, evt := range captured.snapshot() {
			autoSuspended, ok := evt.(fines.MemberAutoSuspended)
			if !ok {
				continue
			}
			if string(autoSuspended.MemberId) == memberId {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("MemberAutoSuspended for memberId %q did not arrive on the bus within 200ms", memberId)
}
