package membership

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
)

// MemberRow is the bun-mapped persistent shape of a member. JSON tags are
// intentionally absent — this struct never crosses the HTTP boundary; the
// HTTP DTOs in internal/membership/http own that.
//
// Column names match migrations/0002_membership.sql verbatim.
type MemberRow struct {
	bun.BaseModel `bun:"table:members"`

	MemberId MemberId         `bun:"member_id,pk"`
	Name     string           `bun:"name,notnull"`
	Email    string           `bun:"email,notnull,unique"`
	Tier     MembershipTier   `bun:"tier,notnull"`
	Status   MembershipStatus `bun:"status,notnull"`
}

// BunRepository is the Postgres-backed Repository implementation. Every
// method satisfies the same contract as InMemoryRepository: Find* returns
// (nil, nil) on miss; non-nil errors signal infrastructure failure.
type BunRepository struct {
	db *bun.DB
}

// Compile-time assertion that BunRepository satisfies Repository. If a
// method signature drifts, the assertion fails before any test runs.
var _ Repository = (*BunRepository)(nil)

// NewBunRepository constructs a BunRepository bound to db. The caller owns
// the *bun.DB lifecycle (open + close); BunRepository does not close it.
func NewBunRepository(db *bun.DB) *BunRepository {
	return &BunRepository{db: db}
}

// SaveMember upserts the member by primary key. Matches the TS source's
// `onConflictDoUpdate` semantics: a save against an existing member_id
// overwrites every column in place.
func (r *BunRepository) SaveMember(ctx context.Context, member MemberDto) error {
	row := toMemberRow(member)
	_, err := r.db.NewInsert().
		Model(&row).
		On("CONFLICT (member_id) DO UPDATE").
		Set("name = EXCLUDED.name").
		Set("email = EXCLUDED.email").
		Set("tier = EXCLUDED.tier").
		Set("status = EXCLUDED.status").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("save member %q: %w", member.MemberId, err)
	}
	return nil
}

// FindMemberById returns the member row by primary key, or (nil, nil) on
// miss.
func (r *BunRepository) FindMemberById(ctx context.Context, memberId MemberId) (*MemberDto, error) {
	var row MemberRow
	err := r.db.NewSelect().Model(&row).Where("member_id = ?", memberId).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find member by id %q: %w", memberId, err)
	}
	member := toMemberDto(row)
	return &member, nil
}

// FindMemberByEmail returns the member row by email, or (nil, nil) on miss.
func (r *BunRepository) FindMemberByEmail(ctx context.Context, email string) (*MemberDto, error) {
	var row MemberRow
	err := r.db.NewSelect().Model(&row).Where("email = ?", email).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find member by email %q: %w", email, err)
	}
	member := toMemberDto(row)
	return &member, nil
}

// toMemberRow converts a domain MemberDto into the bun row.
func toMemberRow(member MemberDto) MemberRow {
	return MemberRow{
		MemberId: member.MemberId,
		Name:     member.Name,
		Email:    member.Email,
		Tier:     member.Tier,
		Status:   member.Status,
	}
}

// toMemberDto converts a bun row back into a domain MemberDto.
func toMemberDto(row MemberRow) MemberDto {
	return MemberDto{
		MemberId: row.MemberId,
		Name:     row.Name,
		Email:    row.Email,
		Tier:     row.Tier,
		Status:   row.Status,
	}
}
