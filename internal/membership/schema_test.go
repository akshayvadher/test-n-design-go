// schema_test.go covers the hand-written ParseNewMember parser. Stdlib
// testing only; same-package so the test can reference *InvalidMemberError
// directly.
package membership

import (
	"errors"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// ParseNewMember — happy path round-trips the trimmed shape; failures
// surface *InvalidMemberError with the expected reason fragment.
// -----------------------------------------------------------------------------

func TestParseNewMember_HappyPathTrims(t *testing.T) {
	dto := NewMemberDto{
		Name:  "  Ada Lovelace  ",
		Email: "  ada.lovelace@example.com  ",
	}

	got, err := ParseNewMember(dto)
	if err != nil {
		t.Fatalf("ParseNewMember: got error %v, want nil", err)
	}
	if got.Name != "Ada Lovelace" {
		t.Errorf("Name: got %q, want %q", got.Name, "Ada Lovelace")
	}
	if got.Email != "ada.lovelace@example.com" {
		t.Errorf("Email: got %q, want %q", got.Email, "ada.lovelace@example.com")
	}
}

func TestParseNewMember_Rejects(t *testing.T) {
	cases := []struct {
		name          string
		dto           NewMemberDto
		wantReasonHas string
	}{
		{
			name:          "blank name",
			dto:           NewMemberDto{Name: "   ", Email: "ada@example.com"},
			wantReasonHas: "name is required",
		},
		{
			name:          "empty name",
			dto:           NewMemberDto{Name: "", Email: "ada@example.com"},
			wantReasonHas: "name is required",
		},
		{
			name:          "blank email",
			dto:           NewMemberDto{Name: "Ada", Email: "   "},
			wantReasonHas: "email is required",
		},
		{
			name:          "empty email",
			dto:           NewMemberDto{Name: "Ada", Email: ""},
			wantReasonHas: "email is required",
		},
		{
			name:          "missing @",
			dto:           NewMemberDto{Name: "Ada", Email: "not-an-email"},
			wantReasonHas: "email format is invalid",
		},
		{
			name:          "missing dotted domain",
			dto:           NewMemberDto{Name: "Ada", Email: "missing@domain"},
			wantReasonHas: "email format is invalid",
		},
		{
			name:          "double @",
			dto:           NewMemberDto{Name: "Ada", Email: "two@@at.com"},
			wantReasonHas: "email format is invalid",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseNewMember(tc.dto)
			assertInvalidMemberReason(t, err, tc.wantReasonHas)
		})
	}
}

// assertInvalidMemberReason fails the test if err is not
// *InvalidMemberError or if its Reason does not contain reasonSubstring.
func assertInvalidMemberReason(t *testing.T, err error, reasonSubstring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("got nil error, want *InvalidMemberError with reason containing %q", reasonSubstring)
	}
	var invalid *InvalidMemberError
	if !errors.As(err, &invalid) {
		t.Fatalf("got %v (%T), want *InvalidMemberError", err, err)
	}
	if !strings.Contains(invalid.Reason, reasonSubstring) {
		t.Errorf("Reason: got %q, want substring %q", invalid.Reason, reasonSubstring)
	}
}
