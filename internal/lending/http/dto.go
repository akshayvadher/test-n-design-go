// Package http is the lending module's HTTP edge. Every type defined here
// is a wire shape — a JSON request body or response envelope — and exists
// only to be encoded/decoded across the network boundary. None of these
// types leak out of this package: the handlers translate inbound DTOs into
// lending domain DTOs via the mapping helpers, and translate outbound
// LoanDto / ReservationDto into response shapes before writing the body.
//
// The JSON tags match the source TypeScript API contract byte-for-byte
// (camelCase) so client integrations work unchanged across the port.
package http

import "time"

// BorrowRequest is the inbound body for POST /loans. The handler builds an
// accesscontrol.AuthUser{MemberID: req.MemberId, Role: accesscontrol.RoleMember}
// from these fields (the demo-auth shortcut matching the TS source).
type BorrowRequest struct {
	MemberId string `json:"memberId"`
	CopyId   string `json:"copyId"`
}

// ReserveRequest is the inbound body for POST /reservations.
type ReserveRequest struct {
	MemberId string `json:"memberId"`
	BookId   string `json:"bookId"`
}

// LoanResponse is the outbound body for every endpoint that returns a
// single loan (POST /loans, PATCH /loans/{loanId}/return). ReturnedAt is
// omitted from the JSON via omitempty when nil — matches the source TS
// optional field.
type LoanResponse struct {
	LoanId     string     `json:"loanId"`
	MemberId   string     `json:"memberId"`
	CopyId     string     `json:"copyId"`
	BookId     string     `json:"bookId"`
	BorrowedAt time.Time  `json:"borrowedAt"`
	DueDate    time.Time  `json:"dueDate"`
	ReturnedAt *time.Time `json:"returnedAt,omitempty"`
}

// ReservationResponse is the outbound body for POST /reservations.
// FulfilledAt is omitted via omitempty when nil.
type ReservationResponse struct {
	ReservationId string     `json:"reservationId"`
	MemberId      string     `json:"memberId"`
	BookId        string     `json:"bookId"`
	ReservedAt    time.Time  `json:"reservedAt"`
	FulfilledAt   *time.Time `json:"fulfilledAt,omitempty"`
}
