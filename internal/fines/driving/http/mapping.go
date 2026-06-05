package http

import (
	"github.com/akshayvadher/test-n-design-go/internal/fines"
)

// toFineResponse translates a fines.FineDto into the outbound HTTP DTO.
// String casts unwrap the FineId / MemberId / LoanId named types so JSON
// encoding sees plain strings. PaidAt's pointer propagates as-is so
// json:"paidAt,omitempty" strips the field when nil.
func toFineResponse(fine fines.FineDto) FineResponse {
	return FineResponse{
		FineId:      string(fine.FineId),
		MemberId:    string(fine.MemberId),
		LoanId:      string(fine.LoanId),
		AmountCents: int64(fine.AmountCents),
		AssessedAt:  fine.AssessedAt,
		PaidAt:      fine.PaidAt,
	}
}

// toFineResponseSlice translates a slice of fines.FineDto into a fresh
// slice of FineResponse, preserving order. A nil input returns an empty
// (non-nil) slice so JSON encoding emits `[]` not `null`.
func toFineResponseSlice(fineSlice []fines.FineDto) []FineResponse {
	out := make([]FineResponse, 0, len(fineSlice))
	for _, fine := range fineSlice {
		out = append(out, toFineResponse(fine))
	}
	return out
}
