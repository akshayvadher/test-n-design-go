// Package http is the fines module's HTTP edge. Every type defined here
// is a wire shape — a JSON request body or response envelope — and exists
// only to be encoded/decoded across the network boundary. None of these
// types leak out of this package: the handlers translate inbound DTOs
// into fines domain DTOs via the mapping helpers, and translate outbound
// FineDto into the response shape before writing the body.
//
// The JSON tags match the source TypeScript API contract byte-for-byte
// (camelCase) so client integrations work unchanged across the port.
package http

import "time"

// FineResponse is the outbound body for every endpoint that returns one
// or many fines. PaidAt is omitted from the JSON via omitempty when nil —
// matches the source TS optional field.
type FineResponse struct {
	FineId      string     `json:"fineId"`
	MemberId    string     `json:"memberId"`
	LoanId      string     `json:"loanId"`
	AmountCents int64      `json:"amountCents"`
	AssessedAt  time.Time  `json:"assessedAt"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
}
