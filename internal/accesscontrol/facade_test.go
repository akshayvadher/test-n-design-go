// facade_test.go covers Slice 6's facade-level ACs end-to-end against the real
// Facade and the unexported policy map. Stdlib testing only, no testify, no
// mocks. The file lives in package accesscontrol (not accesscontrol_test) so
// the data-driven snapshot assertion can read the unexported policy map
// directly.
//
// The ACs exercised:
//
//   - Authorize returns nil for MEMBER calling lending.borrow
//   - Authorize returns *UnauthorizedRoleError (all four fields populated) for
//     ACCOUNT calling lending.borrow
//   - Authorize returns *UnknownActionError for any role calling
//     lending.unknown-action (action missing inside a known module)
//   - Authorize returns *UnknownActionError for any role calling
//     unknown-module.borrow (module missing from policy entirely)
//   - UnauthorizedRoleError.Error() matches regex `role ACCOUNT.*lending\.borrow`
//   - Authorize returns nil for STAFF calling catalog.uploadThumbnail and
//     catalog.removeThumbnail
//   - Authorize returns *UnauthorizedRoleError for MEMBER/ACCOUNT calling
//     catalog.uploadThumbnail and catalog.removeThumbnail
//   - Data-driven snapshot: policy["lending"]["borrow"] == []Role{RoleMember}
//     and policy["catalog"]["uploadThumbnail"] == []Role{RoleStaff}, then
//     Authorize honours the snapshot
//   - SampleStaffAuthUser() returns a STAFF user Authorize accepts; the
//     WithMemberID option overrides ID and preserves role
package accesscontrol

import (
	"errors"
	"regexp"
	"testing"
)

// -----------------------------------------------------------------------------
// Authorize — happy and unauthorized paths over the real policy (table-driven)
//
// Cases 1-2 + 6-7 of the slice ACs share a single table: caller + target →
// expected outcome. Each row is one t.Run subtest, so a failure reports the
// exact (role, module, action) triple that regressed.
// -----------------------------------------------------------------------------

type authorizeOutcome int

const (
	outcomeAllowed authorizeOutcome = iota
	outcomeUnauthorizedRole
)

func TestAuthorize_KnownPolicyEntries(t *testing.T) {
	cases := []struct {
		name     string
		user     AuthUser
		module   ModuleName
		action   ActionName
		outcome  authorizeOutcome
	}{
		{
			name:    "MEMBER may borrow on lending",
			user:    AuthUser{MemberID: "member-1", Role: RoleMember},
			module:  ModuleName("lending"),
			action:  ActionName("borrow"),
			outcome: outcomeAllowed,
		},
		{
			name:    "ACCOUNT may not borrow on lending",
			user:    AuthUser{MemberID: "account-7", Role: RoleAccount},
			module:  ModuleName("lending"),
			action:  ActionName("borrow"),
			outcome: outcomeUnauthorizedRole,
		},
		{
			name:    "STAFF may uploadThumbnail on catalog",
			user:    AuthUser{MemberID: "staff-1", Role: RoleStaff},
			module:  ModuleName("catalog"),
			action:  ActionName("uploadThumbnail"),
			outcome: outcomeAllowed,
		},
		{
			name:    "STAFF may removeThumbnail on catalog",
			user:    AuthUser{MemberID: "staff-1", Role: RoleStaff},
			module:  ModuleName("catalog"),
			action:  ActionName("removeThumbnail"),
			outcome: outcomeAllowed,
		},
		{
			name:    "MEMBER may not uploadThumbnail on catalog",
			user:    AuthUser{MemberID: "member-1", Role: RoleMember},
			module:  ModuleName("catalog"),
			action:  ActionName("uploadThumbnail"),
			outcome: outcomeUnauthorizedRole,
		},
		{
			name:    "MEMBER may not removeThumbnail on catalog",
			user:    AuthUser{MemberID: "member-1", Role: RoleMember},
			module:  ModuleName("catalog"),
			action:  ActionName("removeThumbnail"),
			outcome: outcomeUnauthorizedRole,
		},
		{
			name:    "ACCOUNT may not uploadThumbnail on catalog",
			user:    AuthUser{MemberID: "account-7", Role: RoleAccount},
			module:  ModuleName("catalog"),
			action:  ActionName("uploadThumbnail"),
			outcome: outcomeUnauthorizedRole,
		},
		{
			name:    "ACCOUNT may not removeThumbnail on catalog",
			user:    AuthUser{MemberID: "account-7", Role: RoleAccount},
			module:  ModuleName("catalog"),
			action:  ActionName("removeThumbnail"),
			outcome: outcomeUnauthorizedRole,
		},
	}

	facade := NewFacade()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := facade.Authorize(tc.user, tc.module, tc.action)

			switch tc.outcome {
			case outcomeAllowed:
				if err != nil {
					t.Fatalf("Authorize: got %v, want nil", err)
				}
			case outcomeUnauthorizedRole:
				var roleErr *UnauthorizedRoleError
				if !errors.As(err, &roleErr) {
					t.Fatalf("Authorize: got %v (%T), want *UnauthorizedRoleError", err, err)
				}
				if roleErr.MemberID != tc.user.MemberID {
					t.Errorf("MemberID: got %q, want %q", roleErr.MemberID, tc.user.MemberID)
				}
				if roleErr.Role != tc.user.Role {
					t.Errorf("Role: got %q, want %q", roleErr.Role, tc.user.Role)
				}
				if roleErr.ModuleName != tc.module {
					t.Errorf("ModuleName: got %q, want %q", roleErr.ModuleName, tc.module)
				}
				if roleErr.Action != tc.action {
					t.Errorf("Action: got %q, want %q", roleErr.Action, tc.action)
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// UnknownActionError — action missing inside a known module + module missing
// from policy entirely. Both branches must surface *UnknownActionError so
// errors.As works through middleware wrapping.
// -----------------------------------------------------------------------------

func TestAuthorize_UnknownAction_KnownModuleMissingAction(t *testing.T) {
	facade := NewFacade()

	roles := []Role{RoleMember, RoleAccount, RoleStaff}
	for _, role := range roles {
		role := role
		t.Run(string(role), func(t *testing.T) {
			user := AuthUser{MemberID: "any", Role: role}
			err := facade.Authorize(user, ModuleName("lending"), ActionName("unknown-action"))

			var unknown *UnknownActionError
			if !errors.As(err, &unknown) {
				t.Fatalf("Authorize: got %v (%T), want *UnknownActionError", err, err)
			}
			if unknown.ModuleName != ModuleName("lending") {
				t.Errorf("ModuleName: got %q, want %q", unknown.ModuleName, "lending")
			}
			if unknown.Action != ActionName("unknown-action") {
				t.Errorf("Action: got %q, want %q", unknown.Action, "unknown-action")
			}
		})
	}
}

func TestAuthorize_UnknownAction_ModuleMissingEntirely(t *testing.T) {
	facade := NewFacade()

	roles := []Role{RoleMember, RoleAccount, RoleStaff}
	for _, role := range roles {
		role := role
		t.Run(string(role), func(t *testing.T) {
			user := AuthUser{MemberID: "any", Role: role}
			err := facade.Authorize(user, ModuleName("unknown-module"), ActionName("borrow"))

			var unknown *UnknownActionError
			if !errors.As(err, &unknown) {
				t.Fatalf("Authorize: got %v (%T), want *UnknownActionError", err, err)
			}
			if unknown.ModuleName != ModuleName("unknown-module") {
				t.Errorf("ModuleName: got %q, want %q", unknown.ModuleName, "unknown-module")
			}
			if unknown.Action != ActionName("borrow") {
				t.Errorf("Action: got %q, want %q", unknown.Action, "borrow")
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Error message format — locked by spec to match `role ACCOUNT.*lending\.borrow`
// so log lines and surfaced errors stay grep-able across the codebase.
// -----------------------------------------------------------------------------

func TestUnauthorizedRoleError_MessageMatchesSpecRegex(t *testing.T) {
	facade := NewFacade()
	user := AuthUser{MemberID: "account-7", Role: RoleAccount}

	err := facade.Authorize(user, ModuleName("lending"), ActionName("borrow"))

	var roleErr *UnauthorizedRoleError
	if !errors.As(err, &roleErr) {
		t.Fatalf("Authorize: got %v (%T), want *UnauthorizedRoleError", err, err)
	}

	pattern := regexp.MustCompile(`role ACCOUNT.*lending\.borrow`)
	if !pattern.MatchString(roleErr.Error()) {
		t.Errorf("Error() = %q; want match for %q", roleErr.Error(), pattern.String())
	}
}

// -----------------------------------------------------------------------------
// Data-driven snapshot — the source-of-truth assertion. Pin the unexported
// policy entries the rest of the suite depends on, then prove Authorize honours
// those entries. If a future edit drifts the policy map, this test fails first
// and pinpoints the row that changed.
// -----------------------------------------------------------------------------

func TestPolicy_SnapshotAndAuthorizeHonoursIt(t *testing.T) {
	lendingBorrow := policy[ModuleName("lending")][ActionName("borrow")]
	if !equalRoles(lendingBorrow, []Role{RoleMember}) {
		t.Fatalf("policy[lending][borrow]: got %v, want %v", lendingBorrow, []Role{RoleMember})
	}

	catalogUpload := policy[ModuleName("catalog")][ActionName("uploadThumbnail")]
	if !equalRoles(catalogUpload, []Role{RoleStaff}) {
		t.Fatalf("policy[catalog][uploadThumbnail]: got %v, want %v", catalogUpload, []Role{RoleStaff})
	}

	facade := NewFacade()

	if err := facade.Authorize(SampleAuthUser(), ModuleName("lending"), ActionName("borrow")); err != nil {
		t.Errorf("Authorize MEMBER lending.borrow: got %v, want nil (snapshot honoured)", err)
	}
	if err := facade.Authorize(SampleStaffAuthUser(), ModuleName("catalog"), ActionName("uploadThumbnail")); err != nil {
		t.Errorf("Authorize STAFF catalog.uploadThumbnail: got %v, want nil (snapshot honoured)", err)
	}
}

// -----------------------------------------------------------------------------
// SampleStaffAuthUser — verifies the builder default plus the WithMemberID
// override semantics documented in sample_data.go.
// -----------------------------------------------------------------------------

func TestSampleStaffAuthUser_DefaultsAreAccepted(t *testing.T) {
	user := SampleStaffAuthUser()

	if user.Role != RoleStaff {
		t.Fatalf("default Role: got %q, want %q", user.Role, RoleStaff)
	}

	facade := NewFacade()
	if err := facade.Authorize(user, ModuleName("catalog"), ActionName("uploadThumbnail")); err != nil {
		t.Errorf("Authorize: got %v, want nil for default STAFF user", err)
	}
}

func TestSampleStaffAuthUser_WithMemberIDOverridesIDPreservesRole(t *testing.T) {
	user := SampleStaffAuthUser(WithMemberID("staff-42"))

	if user.MemberID != "staff-42" {
		t.Errorf("MemberID: got %q, want %q", user.MemberID, "staff-42")
	}
	if user.Role != RoleStaff {
		t.Errorf("Role: got %q, want %q (WithMemberID must not touch Role)", user.Role, RoleStaff)
	}
}

// -----------------------------------------------------------------------------
// Small assertion helpers — stdlib only, no testify.
// -----------------------------------------------------------------------------

func equalRoles(a, b []Role) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
