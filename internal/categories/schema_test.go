// schema_test.go covers the hand-written ParseNewCategory and
// ParseStartsWith parsers. Stdlib testing only; same-package so the
// tests can reference the unexported error types directly.
package categories

import (
	"errors"
	"strings"
	"testing"
)

func TestParseNewCategory_HappyPathTrims(t *testing.T) {
	got, err := ParseNewCategory("  Fiction  ")
	if err != nil {
		t.Fatalf("ParseNewCategory: got error %v, want nil", err)
	}
	if got != "Fiction" {
		t.Errorf("Name: got %q, want %q", got, "Fiction")
	}
}

func TestParseNewCategory_Rejects(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantReasonHas string
	}{
		{
			name:          "empty name",
			input:         "",
			wantReasonHas: "name is required",
		},
		{
			name:          "whitespace-only name",
			input:         "   ",
			wantReasonHas: "name is required",
		},
		{
			name:          "name too long",
			input:         strings.Repeat("a", 101),
			wantReasonHas: "name too long",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNewCategory(tc.input)
			assertInvalidCategoryReason(t, err, tc.wantReasonHas)
		})
	}
}

func TestParseNewCategory_AcceptsLengthExactly100(t *testing.T) {
	got, err := ParseNewCategory(strings.Repeat("a", 100))
	if err != nil {
		t.Fatalf("ParseNewCategory(100 chars): got error %v, want nil", err)
	}
	if len(got) != 100 {
		t.Errorf("len: got %d, want %d", len(got), 100)
	}
}

func TestParseStartsWith_HappyPathTrims(t *testing.T) {
	got, err := ParseStartsWith("  fi  ")
	if err != nil {
		t.Fatalf("ParseStartsWith: got error %v, want nil", err)
	}
	if got != "fi" {
		t.Errorf("prefix: got %q, want %q", got, "fi")
	}
}

func TestParseStartsWith_Rejects(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "whitespace-only", input: "   "},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseStartsWith(tc.input)
			assertInvalidCategoriesQueryReason(t, err, "startsWith is required")
		})
	}
}

func assertInvalidCategoryReason(t *testing.T, err error, reasonSubstring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want *InvalidCategoryError with reason containing %q", reasonSubstring)
	}
	var invalid *InvalidCategoryError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v (%T), want *InvalidCategoryError", err, err)
	}
	if !strings.Contains(invalid.Reason, reasonSubstring) {
		t.Errorf("Reason: got %q, want substring %q", invalid.Reason, reasonSubstring)
	}
}

func assertInvalidCategoriesQueryReason(t *testing.T, err error, reasonSubstring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want *InvalidCategoriesQueryError with reason containing %q", reasonSubstring)
	}
	var invalid *InvalidCategoriesQueryError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v (%T), want *InvalidCategoriesQueryError", err, err)
	}
	if !strings.Contains(invalid.Reason, reasonSubstring) {
		t.Errorf("Reason: got %q, want substring %q", invalid.Reason, reasonSubstring)
	}
}
