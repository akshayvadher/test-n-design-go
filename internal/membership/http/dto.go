// Package http is the membership module's HTTP edge. Every type defined
// here is a wire shape — a JSON request body or response envelope — and
// exists only to be encoded/decoded across the network boundary. None of
// these types leak out of this package: the handlers translate inbound
// DTOs into membership.NewMemberDto via the (also-unexported) mapping
// helpers, and translate outbound membership.MemberDto / EligibilityDto
// into response shapes before writing the body.
//
// The JSON tags match the source TypeScript API contract byte-for-byte
// (camelCase, not snake_case) so client integrations work unchanged across
// the port.
package http

// RegisterMemberRequest is the inbound body for POST /members.
type RegisterMemberRequest struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// MemberResponse is the outbound body for every endpoint that returns a
// member.
type MemberResponse struct {
	MemberId string `json:"memberId"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Tier     string `json:"tier"`
	Status   string `json:"status"`
}

// UpgradeTierRequest is the inbound body for PATCH /members/{id}/tier.
type UpgradeTierRequest struct {
	Tier string `json:"tier"`
}

// EligibilityResponse is the outbound body for GET /members/{id}/eligibility.
// Reason is omitempty so an eligible member's JSON does not carry an empty
// `"reason": ""` field.
type EligibilityResponse struct {
	MemberId string `json:"memberId"`
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}
