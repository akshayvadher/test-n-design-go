package membership

import (
	"regexp"
	"strings"
)

// emailFormatPattern is a simple format check: one @, non-empty local part,
// a dotted domain. Not RFC 5322 — good enough for a domain invariant;
// exotic addresses can be rejected at the transport layer if the business
// ever cares. Matches the source TS validator 1:1.
var emailFormatPattern = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)

// ParseNewMember trims the name and email, rejects blanks, and rejects an
// email that does not match the format pattern. On any failure it returns
// the first validator complaint wrapped in *InvalidMemberError. On success
// it returns the trimmed dto.
func ParseNewMember(dto NewMemberDto) (NewMemberDto, error) {
	name := strings.TrimSpace(dto.Name)
	if name == "" {
		return NewMemberDto{}, &InvalidMemberError{Reason: "name is required"}
	}
	email := strings.TrimSpace(dto.Email)
	if email == "" {
		return NewMemberDto{}, &InvalidMemberError{Reason: "email is required"}
	}
	if !emailFormatPattern.MatchString(email) {
		return NewMemberDto{}, &InvalidMemberError{Reason: "email format is invalid: " + email}
	}
	return NewMemberDto{Name: name, Email: email}, nil
}
