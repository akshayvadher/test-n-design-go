package membership

import (
	"context"
	"log/slog"
)

// Facade is the only public surface of the membership module. Every
// business operation — registering a member, suspending, upgrading the
// tier, checking eligibility — goes through one of its exported methods.
// Unexported fields keep its collaborators encapsulated; the composition
// root wires them via NewFacade and tests substitute them via
// NewFacadeWithOverrides.
//
// Unlike catalog, the membership facade does NOT take an accesscontrol
// dependency — it is used by other modules' authorized flows; the
// authorization decision happens at the caller.
type Facade struct {
	repository Repository
	newID      func() string
	logger     *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The composition
// root passes the concrete implementations; tests use
// NewFacadeWithOverrides which fills the same arguments from an Overrides
// struct with in-memory defaults.
func NewFacade(repository Repository, newID func() string, logger *slog.Logger) *Facade {
	return &Facade{
		repository: repository,
		newID:      newID,
		logger:     logger,
	}
}

// RegisterMember validates the dto, rejects a duplicate email, mints a
// fresh MemberId, persists the member with default Tier STANDARD and
// Status ACTIVE, and returns the saved value.
func (f *Facade) RegisterMember(ctx context.Context, dto NewMemberDto) (MemberDto, error) {
	parsed, err := ParseNewMember(dto)
	if err != nil {
		return MemberDto{}, err
	}
	existing, err := f.repository.FindMemberByEmail(ctx, parsed.Email)
	if err != nil {
		return MemberDto{}, err
	}
	if existing != nil {
		return MemberDto{}, &DuplicateEmailError{Email: parsed.Email}
	}
	member := MemberDto{
		MemberId: MemberId(f.newID()),
		Name:     parsed.Name,
		Email:    parsed.Email,
		Tier:     MembershipTierStandard,
		Status:   MembershipStatusActive,
	}
	if err := f.repository.SaveMember(ctx, member); err != nil {
		return MemberDto{}, err
	}
	return member, nil
}

// FindMember loads the member by id. Unknown id returns *MemberNotFoundError.
func (f *Facade) FindMember(ctx context.Context, memberId MemberId) (MemberDto, error) {
	member, err := f.repository.FindMemberById(ctx, memberId)
	if err != nil {
		return MemberDto{}, err
	}
	if member == nil {
		return MemberDto{}, &MemberNotFoundError{Identifier: string(memberId)}
	}
	return *member, nil
}

// Suspend flips the member's status to SUSPENDED.
func (f *Facade) Suspend(ctx context.Context, memberId MemberId) (MemberDto, error) {
	return f.updateMemberStatus(ctx, memberId, MembershipStatusSuspended)
}

// Reactivate flips the member's status to ACTIVE.
func (f *Facade) Reactivate(ctx context.Context, memberId MemberId) (MemberDto, error) {
	return f.updateMemberStatus(ctx, memberId, MembershipStatusActive)
}

// UpgradeTier sets the member's tier to the supplied value, persists the
// updated record, and returns it. Unknown id returns *MemberNotFoundError.
func (f *Facade) UpgradeTier(ctx context.Context, memberId MemberId, tier MembershipTier) (MemberDto, error) {
	existing, err := f.repository.FindMemberById(ctx, memberId)
	if err != nil {
		return MemberDto{}, err
	}
	if existing == nil {
		return MemberDto{}, &MemberNotFoundError{Identifier: string(memberId)}
	}
	updated := *existing
	updated.Tier = tier
	if err := f.repository.SaveMember(ctx, updated); err != nil {
		return MemberDto{}, err
	}
	return updated, nil
}

// CheckEligibility loads the member and reports eligibility. A SUSPENDED
// member is not eligible with Reason "SUSPENDED"; every other status is
// eligible with empty Reason.
func (f *Facade) CheckEligibility(ctx context.Context, memberId MemberId) (EligibilityDto, error) {
	member, err := f.repository.FindMemberById(ctx, memberId)
	if err != nil {
		return EligibilityDto{}, err
	}
	if member == nil {
		return EligibilityDto{}, &MemberNotFoundError{Identifier: string(memberId)}
	}
	if member.Status == MembershipStatusSuspended {
		return EligibilityDto{MemberId: memberId, Eligible: false, Reason: "SUSPENDED"}, nil
	}
	return EligibilityDto{MemberId: memberId, Eligible: true}, nil
}

// updateMemberStatus loads the member, flips its status, and saves it
// back. Unknown id returns *MemberNotFoundError. Shared by Suspend and
// Reactivate so the two methods don't repeat the load-modify-save dance.
func (f *Facade) updateMemberStatus(ctx context.Context, memberId MemberId, status MembershipStatus) (MemberDto, error) {
	existing, err := f.repository.FindMemberById(ctx, memberId)
	if err != nil {
		return MemberDto{}, err
	}
	if existing == nil {
		return MemberDto{}, &MemberNotFoundError{Identifier: string(memberId)}
	}
	updated := *existing
	updated.Status = status
	if err := f.repository.SaveMember(ctx, updated); err != nil {
		return MemberDto{}, err
	}
	return updated, nil
}
