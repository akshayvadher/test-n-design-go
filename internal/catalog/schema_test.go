// schema_test.go covers Slice 1's hand-written parsers: ParseIsbn,
// ParseNewBook, ParseUpdateBook, ParseNewCopy. Stdlib testing only; the file
// lives in package catalog so it can reference the unexported helpers if a
// future regression needs to.
//
// Test scope (Slice 1 minimum-viable foundation — the full facade-level spec
// lands in Slice 2):
//
//   - happy paths (valid title/authors/isbn, valid update patches, valid copy
//     condition)
//   - blank / whitespace rejection on title, authors and isbn
//   - ISBN-10 and ISBN-13 acceptance with and without hyphens/spaces; rejection
//     of malformed inputs ("123", "not-an-isbn", arbitrary length)
//   - update-patch nil/non-nil combinations including "both nil" rejection and
//     "explicit empty Authors slice" rejection
//   - typed-error assertions via errors.As against *InvalidBookError /
//     *InvalidCopyError so middleware mapping in Slice 3 keeps working
package catalog

import (
	"errors"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// ParseIsbn — happy paths (ISBN-10 / ISBN-13, hyphens, spaces) and rejections
// (blank, malformed, wrong length). Table-driven so a regression names the
// exact input that drifted.
// -----------------------------------------------------------------------------

func TestParseIsbn_Accepts(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want Isbn
	}{
		{name: "ISBN-13 hyphenated", raw: "978-0135957059", want: Isbn("978-0135957059")},
		{name: "ISBN-13 plain", raw: "9780135957059", want: Isbn("9780135957059")},
		{name: "ISBN-13 with spaces", raw: "978 013 595 7059", want: Isbn("978 013 595 7059")},
		{name: "ISBN-10 hyphenated", raw: "0-306-40615-2", want: Isbn("0-306-40615-2")},
		{name: "ISBN-10 plain", raw: "0306406152", want: Isbn("0306406152")},
		{name: "ISBN-10 trailing X", raw: "097522980X", want: Isbn("097522980X")},
		{name: "ISBN-10 hyphenated trailing X", raw: "0-9752298-0-X", want: Isbn("0-9752298-0-X")},
		{name: "surrounding whitespace trimmed", raw: "  978-0135957059  ", want: Isbn("978-0135957059")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIsbn(tc.raw)
			if err != nil {
				t.Fatalf("ParseIsbn(%q): got error %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("ParseIsbn(%q): got %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseIsbn_Rejects(t *testing.T) {
	cases := []struct {
		name          string
		raw           string
		wantReasonHas string
	}{
		{name: "empty string", raw: "", wantReasonHas: "isbn is required"},
		{name: "whitespace only", raw: "   ", wantReasonHas: "isbn is required"},
		{name: "too short", raw: "123", wantReasonHas: "isbn format is invalid"},
		{name: "letters", raw: "not-an-isbn", wantReasonHas: "isbn format is invalid"},
		{name: "11 digits (between 10 and 13)", raw: "12345678901", wantReasonHas: "isbn format is invalid"},
		{name: "14 digits (too long)", raw: "12345678901234", wantReasonHas: "isbn format is invalid"},
		{name: "ISBN-10 with X in middle", raw: "0X06406152", wantReasonHas: "isbn format is invalid"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseIsbn(tc.raw)
			if got != "" {
				t.Errorf("ParseIsbn(%q): got value %q, want empty on error", tc.raw, got)
			}
			assertInvalidBookError(t, err, tc.wantReasonHas)
		})
	}
}

// -----------------------------------------------------------------------------
// ParseNewBook — happy path round-trips the trimmed/filtered shape; failures
// surface *InvalidBookError with the expected reason fragment.
// -----------------------------------------------------------------------------

func TestParseNewBook_HappyPathTrimsAndFilters(t *testing.T) {
	dto := NewBookDto{
		Title:   "  The Pragmatic Programmer  ",
		Authors: []string{"  Andrew Hunt  ", "", "   ", "David Thomas"},
		Isbn:    Isbn("  978-0135957059  "),
	}

	got, err := ParseNewBook(dto)
	if err != nil {
		t.Fatalf("ParseNewBook: got error %v, want nil", err)
	}
	if got.Title != "The Pragmatic Programmer" {
		t.Errorf("Title: got %q, want %q", got.Title, "The Pragmatic Programmer")
	}
	wantAuthors := []string{"Andrew Hunt", "David Thomas"}
	if !equalStrings(got.Authors, wantAuthors) {
		t.Errorf("Authors: got %v, want %v", got.Authors, wantAuthors)
	}
	if got.Isbn != Isbn("978-0135957059") {
		t.Errorf("Isbn: got %q, want %q", got.Isbn, "978-0135957059")
	}
}

func TestParseNewBook_Rejects(t *testing.T) {
	cases := []struct {
		name          string
		dto           NewBookDto
		wantReasonHas string
	}{
		{
			name:          "blank title",
			dto:           NewBookDto{Title: "   ", Authors: []string{"Author"}, Isbn: Isbn("978-0135957059")},
			wantReasonHas: "title is required",
		},
		{
			name:          "empty title",
			dto:           NewBookDto{Title: "", Authors: []string{"Author"}, Isbn: Isbn("978-0135957059")},
			wantReasonHas: "title is required",
		},
		{
			name:          "nil authors",
			dto:           NewBookDto{Title: "Title", Authors: nil, Isbn: Isbn("978-0135957059")},
			wantReasonHas: "at least one author is required",
		},
		{
			name:          "empty authors slice",
			dto:           NewBookDto{Title: "Title", Authors: []string{}, Isbn: Isbn("978-0135957059")},
			wantReasonHas: "at least one author is required",
		},
		{
			name:          "all-blank authors",
			dto:           NewBookDto{Title: "Title", Authors: []string{"   ", ""}, Isbn: Isbn("978-0135957059")},
			wantReasonHas: "at least one author is required",
		},
		{
			name:          "blank isbn",
			dto:           NewBookDto{Title: "Title", Authors: []string{"Author"}, Isbn: Isbn("   ")},
			wantReasonHas: "isbn is required",
		},
		{
			name:          "malformed isbn",
			dto:           NewBookDto{Title: "Title", Authors: []string{"Author"}, Isbn: Isbn("not-an-isbn")},
			wantReasonHas: "isbn format is invalid",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNewBook(tc.dto)
			assertInvalidBookError(t, err, tc.wantReasonHas)
		})
	}
}

// -----------------------------------------------------------------------------
// ParseUpdateBook — covers the four pointer-field combinations (both nil,
// title-only, authors-only, both set) and per-field blank-rejection.
// -----------------------------------------------------------------------------

func TestParseUpdateBook_HappyPaths(t *testing.T) {
	t.Run("title-only patch trims", func(t *testing.T) {
		title := "  New Title  "
		got, err := ParseUpdateBook(UpdateBookDto{Title: &title})
		if err != nil {
			t.Fatalf("ParseUpdateBook: got error %v, want nil", err)
		}
		if got.Authors != nil {
			t.Errorf("Authors: got %v, want nil (untouched)", got.Authors)
		}
		if got.Title == nil {
			t.Fatalf("Title: got nil, want non-nil pointer")
		}
		if *got.Title != "New Title" {
			t.Errorf("Title: got %q, want %q", *got.Title, "New Title")
		}
	})

	t.Run("authors-only patch trims and filters", func(t *testing.T) {
		authors := []string{"  A  ", "", "B"}
		got, err := ParseUpdateBook(UpdateBookDto{Authors: &authors})
		if err != nil {
			t.Fatalf("ParseUpdateBook: got error %v, want nil", err)
		}
		if got.Title != nil {
			t.Errorf("Title: got %v, want nil (untouched)", got.Title)
		}
		if got.Authors == nil {
			t.Fatalf("Authors: got nil, want non-nil pointer")
		}
		wantAuthors := []string{"A", "B"}
		if !equalStrings(*got.Authors, wantAuthors) {
			t.Errorf("Authors: got %v, want %v", *got.Authors, wantAuthors)
		}
	})

	t.Run("both fields set", func(t *testing.T) {
		title := "T"
		authors := []string{"A"}
		got, err := ParseUpdateBook(UpdateBookDto{Title: &title, Authors: &authors})
		if err != nil {
			t.Fatalf("ParseUpdateBook: got error %v, want nil", err)
		}
		if got.Title == nil || *got.Title != "T" {
			t.Errorf("Title: got %v, want pointer to %q", got.Title, "T")
		}
		if got.Authors == nil || !equalStrings(*got.Authors, []string{"A"}) {
			t.Errorf("Authors: got %v, want pointer to [\"A\"]", got.Authors)
		}
	})
}

func TestParseUpdateBook_Rejects(t *testing.T) {
	blankTitle := "   "
	emptyAuthors := []string{}
	allBlankAuthors := []string{"  ", ""}

	cases := []struct {
		name          string
		dto           UpdateBookDto
		wantReasonHas string
	}{
		{
			name:          "both nil",
			dto:           UpdateBookDto{Title: nil, Authors: nil},
			wantReasonHas: "at least one of title or authors must be provided",
		},
		{
			name:          "blank title",
			dto:           UpdateBookDto{Title: &blankTitle},
			wantReasonHas: "title is required",
		},
		{
			name:          "empty authors slice",
			dto:           UpdateBookDto{Authors: &emptyAuthors},
			wantReasonHas: "at least one author is required",
		},
		{
			name:          "all-blank authors",
			dto:           UpdateBookDto{Authors: &allBlankAuthors},
			wantReasonHas: "at least one author is required",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseUpdateBook(tc.dto)
			assertInvalidBookError(t, err, tc.wantReasonHas)
		})
	}
}

// -----------------------------------------------------------------------------
// ParseNewCopy — accepts the four documented conditions, rejects everything
// else with *InvalidCopyError.
// -----------------------------------------------------------------------------

func TestParseNewCopy_AcceptsAllDocumentedConditions(t *testing.T) {
	conditions := []CopyCondition{
		CopyConditionNew,
		CopyConditionGood,
		CopyConditionFair,
		CopyConditionPoor,
	}
	for _, condition := range conditions {
		condition := condition
		t.Run(string(condition), func(t *testing.T) {
			dto := NewCopyDto{BookId: BookId("book-1"), Condition: condition}
			got, err := ParseNewCopy(dto)
			if err != nil {
				t.Fatalf("ParseNewCopy(%q): got error %v, want nil", condition, err)
			}
			if got.Condition != condition {
				t.Errorf("Condition: got %q, want %q", got.Condition, condition)
			}
			if got.BookId != BookId("book-1") {
				t.Errorf("BookId: got %q, want %q", got.BookId, "book-1")
			}
		})
	}
}

func TestParseNewCopy_RejectsInvalidCondition(t *testing.T) {
	cases := []struct {
		name      string
		condition CopyCondition
	}{
		{name: "empty", condition: CopyCondition("")},
		{name: "BROKEN (not in enum)", condition: CopyCondition("BROKEN")},
		{name: "lowercase good", condition: CopyCondition("good")},
		{name: "arbitrary string", condition: CopyCondition("MINT")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNewCopy(NewCopyDto{BookId: BookId("book-1"), Condition: tc.condition})
			var invalid *InvalidCopyError
			if !errors.As(err, &invalid) {
				t.Fatalf("ParseNewCopy(%q): got %v (%T), want *InvalidCopyError", tc.condition, err, err)
			}
			if !strings.Contains(invalid.Reason, "condition must be one of") {
				t.Errorf("Reason: got %q, want substring %q", invalid.Reason, "condition must be one of")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Small assertion helpers — stdlib only, no testify.
// -----------------------------------------------------------------------------

func assertInvalidBookError(t *testing.T, err error, reasonSubstring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want *InvalidBookError with reason containing %q", reasonSubstring)
	}
	var invalid *InvalidBookError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v (%T), want *InvalidBookError", err, err)
	}
	if !strings.Contains(invalid.Reason, reasonSubstring) {
		t.Errorf("Reason: got %q, want substring %q", invalid.Reason, reasonSubstring)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
