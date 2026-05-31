package bun

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	upstreambun "github.com/uptrace/bun"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
)

// MemberRow is the bun-mapped persistent shape of a member. JSON tags are
// intentionally absent — this struct never crosses the HTTP boundary;
// the HTTP DTOs in internal/membership/driving/http own that.
//
// Column names match migrations/0002_membership.sql verbatim.
type MemberRow struct {
	upstreambun.BaseModel `bun:"table:members"`

	MemberId membership.MemberId         `bun:"member_id,pk"`
	Name     string                      `bun:"name,notnull"`
	Email    string                      `bun:"email,notnull,unique"`
	Tier     membership.MembershipTier   `bun:"tier,notnull"`
	Status   membership.MembershipStatus `bun:"status,notnull"`
}

// Repository is the Postgres-backed membership.Repository implementation.
// Every method satisfies the same contract as the in-memory Repository:
// Find* returns (nil, nil) on miss; non-nil errors signal infrastructure
// failure.
type Repository struct {
	db *upstreambun.DB
}

// Compile-time assertion that *Repository satisfies the membership
// driven port. If a method signature drifts, the assertion fails before
// any test runs.
var _ membership.Repository = (*Repository)(nil)

// NewRepository constructs a *Repository bound to db. The caller owns
// the *bun.DB lifecycle (open + close); Repository does not close it.
func NewRepository(db *upstreambun.DB) *Repository {
	return &Repository{db: db}
}

// SaveMember upserts the member by primary key. Matches the TS source's
// `onConflictDoUpdate` semantics: a save against an existing member_id
// overwrites every column in place.
func (r *Repository) SaveMember(ctx context.Context, member membership.MemberDto) error {
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
func (r *Repository) FindMemberById(ctx context.Context, memberId membership.MemberId) (*membership.MemberDto, error) {
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
func (r *Repository) FindMemberByEmail(ctx context.Context, email string) (*membership.MemberDto, error) {
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
func toMemberRow(member membership.MemberDto) MemberRow {
	return MemberRow{
		MemberId: member.MemberId,
		Name:     member.Name,
		Email:    member.Email,
		Tier:     member.Tier,
		Status:   member.Status,
	}
}

// toMemberDto converts a bun row back into a domain MemberDto.
func toMemberDto(row MemberRow) membership.MemberDto {
	return membership.MemberDto{
		MemberId: row.MemberId,
		Name:     row.Name,
		Email:    row.Email,
		Tier:     row.Tier,
		Status:   row.Status,
	}
}
