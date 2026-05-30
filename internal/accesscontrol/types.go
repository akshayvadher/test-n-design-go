package accesscontrol

import "fmt"

// Role is the closed set of roles a caller can present. The string values are
// uppercase to match the source TS bus and to read clearly in error messages
// emitted by Authorize.
type Role string

// Role values. These are the only Role literals the rest of the codebase
// should reference — never raw string literals.
const (
	RoleMember  Role = "MEMBER"
	RoleAccount Role = "ACCOUNT"
	RoleStaff   Role = "STAFF"
)

// ModuleName is the name of a bounded-context module ("lending", "catalog",
// ...) as it appears in the policy map. Named so call sites cannot accidentally
// swap module and action without a compiler complaint.
type ModuleName string

// ActionName is the name of a gated action within a module ("borrow",
// "uploadThumbnail", ...) as it appears in the policy map. Named for the same
// type-safety reason as ModuleName.
type ActionName string

// AuthUser is the caller identity Authorize evaluates. MemberID is a plain
// string in Phase 1: the canonical MemberId newtype lands in
// internal/membership in Phase 2, and access-control deliberately keeps it
// string to avoid an import cycle from membership → accesscontrol.
type AuthUser struct {
	MemberID string
	Role     Role
}

// UnauthorizedRoleError is returned by Authorize when the policy entry for
// (ModuleName, Action) exists but does not include the caller's Role. All
// fields are populated from the caller's inputs so log lines and surfaced
// errors can identify the offending request.
//
// The Error() format mirrors the source TS message verbatim.
type UnauthorizedRoleError struct {
	MemberID   string
	Role       Role
	ModuleName ModuleName
	Action     ActionName
}

// Error implements the error interface on a pointer receiver so errors.As
// resolves *UnauthorizedRoleError targets.
func (e *UnauthorizedRoleError) Error() string {
	return fmt.Sprintf(
		"role %s is not authorized to perform %s.%s (memberID: %s)",
		e.Role, e.ModuleName, e.Action, e.MemberID,
	)
}

// UnknownActionError is returned by Authorize when policy[ModuleName] is nil
// or policy[ModuleName][Action] is nil — i.e. the (module, action) pair has
// no entry in the data-driven policy map. Matches the source TS class name.
type UnknownActionError struct {
	ModuleName ModuleName
	Action     ActionName
}

// Error implements the error interface on a pointer receiver so errors.As
// resolves *UnknownActionError targets.
func (e *UnknownActionError) Error() string {
	return fmt.Sprintf(
		"unknown action %s.%s — no policy defined",
		e.ModuleName, e.Action,
	)
}
