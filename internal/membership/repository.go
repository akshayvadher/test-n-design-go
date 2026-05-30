package membership

import "context"

// Repository is the persistence port the membership facade depends on. The
// in-memory adapter and the bun-backed Postgres adapter both satisfy it.
//
// Find* methods return (nil, nil) on "no rows" — the facade is responsible
// for translating that into MemberNotFoundError or DuplicateEmailError. A
// non-nil error indicates infrastructure failure (decode, transport, …)
// and is propagated unchanged.
type Repository interface {
	SaveMember(ctx context.Context, member MemberDto) error
	FindMemberById(ctx context.Context, memberId MemberId) (*MemberDto, error)
	FindMemberByEmail(ctx context.Context, email string) (*MemberDto, error)
}
