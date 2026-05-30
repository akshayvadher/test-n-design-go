package accesscontrol

// policy is the data-driven RBAC table the Facade reads. Each entry says: for
// this (ModuleName, ActionName), these Roles are allowed.
//
// Invariant: no role logic is hardcoded in any business module. Adding a new
// gated action means adding a row here, nothing else — the Facade reads this
// map and never branches on Role in business code. This invariant cannot be
// enforced mechanically in Phase 1 (no business modules exist yet); the
// per-phase code review carries the check forward.
//
// policy is unexported on purpose: callers reach it only through Facade.
// Authorize. The same-package test file may reference it directly for the
// data-driven snapshot assertion.
var policy = map[ModuleName]map[ActionName][]Role{
	"lending": {
		"borrow": {RoleMember},
	},
	"catalog": {
		"uploadThumbnail": {RoleStaff},
		"removeThumbnail": {RoleStaff},
	},
}
