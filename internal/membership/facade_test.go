// facade_test.go is the facade-level spec for the membership module — a
// 1:1 port of apps/library/src/membership/membership.facade.spec.ts from
// the source TypeScript repository.
//
// Lives in package membership_test (external test package) so it can
// import the in-memory adapter from internal/membership/driven/memory
// without creating an import cycle. Every symbol is qualified with the
// membership.* prefix.
//
// Stdlib testing only — t.Run for nested describe blocks, errors.As for
// typed-error assertions, no testify, no mock library.
package membership_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/membership"
	membershipmemory "github.com/akshayvadher/test-n-design-go/internal/membership/driven/memory"
)

// -----------------------------------------------------------------------------
// Test helpers
// -----------------------------------------------------------------------------

// sequentialIds returns a deterministic id generator over a closed counter
// so minted MemberId values are predictable in assertions. Default prefix
// is "member". Mirrors the TS source's sequentialIds.
func sequentialIds(prefix string) func() string {
	if prefix == "" {
		prefix = "member"
	}
	counter := 0
	return func() string {
		counter++
		return prefix + "-" + itoa(counter)
	}
}

// itoa is a tiny non-allocating int→string used only by sequentialIds so
// the closure does not pull strconv into otherwise-pure tests. Counter is
// bounded by the test count (low hundreds at most).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}

// buildFacade constructs a Facade with deterministic ids and the default
// in-memory substrates from driven/memory/configuration.go. Mirrors the
// TS buildFacade.
func buildFacade(t *testing.T) *membership.Facade {
	t.Helper()
	return membershipmemory.NewFacadeWithOverrides(membershipmemory.Overrides{NewID: sequentialIds("member")})
}

// mustRegisterMember is a tiny helper for arrange-phase calls where a
// t.Fatalf on failure is cleaner than a four-line err check.
func mustRegisterMember(t *testing.T, facade *membership.Facade, dto membership.NewMemberDto) membership.MemberDto {
	t.Helper()
	member, err := facade.RegisterMember(context.Background(), dto)
	if err != nil {
		t.Fatalf("RegisterMember(%+v) returned unexpected error: %v", dto, err)
	}
	return member
}

// assertInvalidMember fails the test if err is not *InvalidMemberError.
func assertInvalidMember(t *testing.T, err error) {
	t.Helper()
	var target *membership.InvalidMemberError
	if !errors.As(err, &target) {
		t.Fatalf("expected *InvalidMemberError, got %T (%v)", err, err)
	}
}

// assertMemberNotFound fails the test if err is not *MemberNotFoundError.
func assertMemberNotFound(t *testing.T, err error) {
	t.Helper()
	var target *membership.MemberNotFoundError
	if !errors.As(err, &target) {
		t.Fatalf("expected *MemberNotFoundError, got %T (%v)", err, err)
	}
}

// assertDuplicateEmail fails the test if err is not *DuplicateEmailError.
func assertDuplicateEmail(t *testing.T, err error) {
	t.Helper()
	var target *membership.DuplicateEmailError
	if !errors.As(err, &target) {
		t.Fatalf("expected *DuplicateEmailError, got %T (%v)", err, err)
	}
}

// -----------------------------------------------------------------------------
// MembershipFacade — full spec port (1:1 with membership.facade.spec.ts)
// -----------------------------------------------------------------------------

func TestMembershipFacade(t *testing.T) {
	ctx := context.Background()

	t.Run("registers a member with an id, STANDARD tier, and ACTIVE status by default", func(t *testing.T) {
		facade := buildFacade(t)

		member, err := facade.RegisterMember(ctx, membership.SampleNewMember())
		if err != nil {
			t.Fatalf("RegisterMember failed: %v", err)
		}

		if member.MemberId == "" {
			t.Errorf("MemberId: got empty, want non-empty")
		}
		if member.Tier != membership.MembershipTierStandard {
			t.Errorf("Tier: got %q, want %q", member.Tier, membership.MembershipTierStandard)
		}
		if member.Status != membership.MembershipStatusActive {
			t.Errorf("Status: got %q, want %q", member.Status, membership.MembershipStatusActive)
		}
	})

	t.Run("finds a registered member by memberId", func(t *testing.T) {
		facade := buildFacade(t)
		registered := mustRegisterMember(t, facade, membership.SampleNewMember())

		found, err := facade.FindMember(ctx, registered.MemberId)
		if err != nil {
			t.Fatalf("FindMember failed: %v", err)
		}
		if !reflect.DeepEqual(found, registered) {
			t.Errorf("FindMember returned %+v, want %+v", found, registered)
		}
	})

	t.Run("suspends an active member", func(t *testing.T) {
		facade := buildFacade(t)
		member := mustRegisterMember(t, facade, membership.SampleNewMember())

		suspended, err := facade.Suspend(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("Suspend failed: %v", err)
		}
		if suspended.Status != membership.MembershipStatusSuspended {
			t.Errorf("returned status: got %q, want %q", suspended.Status, membership.MembershipStatusSuspended)
		}

		found, err := facade.FindMember(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("FindMember failed: %v", err)
		}
		if found.Status != membership.MembershipStatusSuspended {
			t.Errorf("FindMember status: got %q, want %q", found.Status, membership.MembershipStatusSuspended)
		}
	})

	t.Run("reactivates a suspended member", func(t *testing.T) {
		facade := buildFacade(t)
		member := mustRegisterMember(t, facade, membership.SampleNewMember())
		if _, err := facade.Suspend(ctx, member.MemberId); err != nil {
			t.Fatalf("Suspend failed: %v", err)
		}

		reactivated, err := facade.Reactivate(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("Reactivate failed: %v", err)
		}
		if reactivated.Status != membership.MembershipStatusActive {
			t.Errorf("returned status: got %q, want %q", reactivated.Status, membership.MembershipStatusActive)
		}

		found, err := facade.FindMember(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("FindMember failed: %v", err)
		}
		if found.Status != membership.MembershipStatusActive {
			t.Errorf("FindMember status: got %q, want %q", found.Status, membership.MembershipStatusActive)
		}
	})

	t.Run("upgrades a member tier from STANDARD to PREMIUM", func(t *testing.T) {
		facade := buildFacade(t)
		member := mustRegisterMember(t, facade, membership.SampleNewMember())

		upgraded, err := facade.UpgradeTier(ctx, member.MemberId, membership.MembershipTierPremium)
		if err != nil {
			t.Fatalf("UpgradeTier failed: %v", err)
		}
		if upgraded.Tier != membership.MembershipTierPremium {
			t.Errorf("returned tier: got %q, want %q", upgraded.Tier, membership.MembershipTierPremium)
		}

		found, err := facade.FindMember(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("FindMember failed: %v", err)
		}
		if found.Tier != membership.MembershipTierPremium {
			t.Errorf("FindMember tier: got %q, want %q", found.Tier, membership.MembershipTierPremium)
		}
	})

	t.Run("reports an active member as eligible", func(t *testing.T) {
		facade := buildFacade(t)
		member := mustRegisterMember(t, facade, membership.SampleNewMember())

		eligibility, err := facade.CheckEligibility(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("CheckEligibility failed: %v", err)
		}
		if !eligibility.Eligible {
			t.Errorf("Eligible: got false, want true")
		}
		if eligibility.MemberId != member.MemberId {
			t.Errorf("MemberId: got %q, want %q", eligibility.MemberId, member.MemberId)
		}
	})

	t.Run("reports a suspended member as ineligible with reason SUSPENDED", func(t *testing.T) {
		facade := buildFacade(t)
		member := mustRegisterMember(t, facade, membership.SampleNewMember())
		if _, err := facade.Suspend(ctx, member.MemberId); err != nil {
			t.Fatalf("Suspend failed: %v", err)
		}

		eligibility, err := facade.CheckEligibility(ctx, member.MemberId)
		if err != nil {
			t.Fatalf("CheckEligibility failed: %v", err)
		}
		if eligibility.Eligible {
			t.Errorf("Eligible: got true, want false")
		}
		if eligibility.Reason != "SUSPENDED" {
			t.Errorf("Reason: got %q, want %q", eligibility.Reason, "SUSPENDED")
		}
	})

	t.Run("rejects registering a member with an empty name", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithName("")))
		assertInvalidMember(t, err)

		_, err = facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithName("   ")))
		assertInvalidMember(t, err)
	})

	t.Run("rejects registering a member with a malformed email", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithEmail("")))
		assertInvalidMember(t, err)

		_, err = facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithEmail("not-an-email")))
		assertInvalidMember(t, err)

		_, err = facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithEmail("missing@domain")))
		assertInvalidMember(t, err)

		_, err = facade.RegisterMember(ctx, membership.SampleNewMember(membership.WithEmail("two@@at.com")))
		assertInvalidMember(t, err)
	})

	t.Run("trims surrounding whitespace from name and email on registration", func(t *testing.T) {
		facade := buildFacade(t)

		member, err := facade.RegisterMember(ctx, membership.SampleNewMember(
			membership.WithName("  Ada Lovelace  "),
			membership.WithEmail("  ada@example.com  "),
		))
		if err != nil {
			t.Fatalf("RegisterMember failed: %v", err)
		}
		if member.Name != "Ada Lovelace" {
			t.Errorf("Name: got %q, want %q", member.Name, "Ada Lovelace")
		}
		if member.Email != "ada@example.com" {
			t.Errorf("Email: got %q, want %q", member.Email, "ada@example.com")
		}
	})

	t.Run("rejects registering a member with an email that already exists", func(t *testing.T) {
		facade := buildFacade(t)
		mustRegisterMember(t, facade, membership.SampleNewMemberWithEmail("ada.lovelace@example.com"))

		_, err := facade.RegisterMember(ctx, membership.SampleNewMemberWithEmail("ada.lovelace@example.com"))
		assertDuplicateEmail(t, err)
	})

	t.Run("throws MemberNotFoundError when suspending an unknown member", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.Suspend(ctx, membership.MemberId("unknown-member-id"))
		assertMemberNotFound(t, err)
	})

	t.Run("throws MemberNotFoundError when reactivating an unknown member", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.Reactivate(ctx, membership.MemberId("unknown-member-id"))
		assertMemberNotFound(t, err)
	})

	t.Run("throws MemberNotFoundError when upgrading the tier of an unknown member", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.UpgradeTier(ctx, membership.MemberId("unknown-member-id"), membership.MembershipTierPremium)
		assertMemberNotFound(t, err)
	})

	t.Run("throws MemberNotFoundError when checking eligibility of an unknown member", func(t *testing.T) {
		facade := buildFacade(t)

		_, err := facade.CheckEligibility(ctx, membership.MemberId("unknown-member-id"))
		assertMemberNotFound(t, err)
	})
}
