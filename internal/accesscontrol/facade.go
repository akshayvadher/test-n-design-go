// Package accesscontrol is the data-driven RBAC module for the library
// service. Its only public surface is the Facade, which exposes a single
// Authorize method that checks whether an AuthUser may perform a given
// (ModuleName, ActionName) against the unexported policy map.
//
// Unlike the other modules in Phase 1+, accesscontrol intentionally ships no
// module.go and no http/ sub-package: it exposes no HTTP routes and is wired
// purely by constructor injection into other modules' facades. When Phase 2+
// modules need authorization they take *Facade as a constructor dependency
// and call Authorize at the entry of each gated method.
//
// No init(), no globals beyond the unexported policy map. The package depends
// on the standard library only.
package accesscontrol

// Facade is the only public surface of the accesscontrol module. It has no
// fields in Phase 1 — the policy map is package-level — but the struct exists
// so callers depend on a concrete type they can substitute via Overrides as
// the module grows.
type Facade struct{}

// NewFacade constructs the Phase 1 Facade. There is no configuration to wire:
// the policy map is data-driven and immutable at runtime. The constructor
// exists so the composition root has a single, named entry point that
// matches the pattern other modules follow.
func NewFacade() *Facade {
	return &Facade{}
}

// Authorize checks whether authUser may perform (moduleName, action) against
// the data-driven policy map. It returns:
//
//   - nil when policy[moduleName][action] contains authUser.Role;
//   - *UnknownActionError when policy[moduleName] is nil or
//     policy[moduleName][action] is nil — the (module, action) pair has no
//     entry in the policy;
//   - *UnauthorizedRoleError when the policy entry exists but does not include
//     authUser.Role.
//
// Both error types are pointer-receiver implementations so errors.As resolves
// them through wrapping layers.
func (f *Facade) Authorize(authUser AuthUser, moduleName ModuleName, action ActionName) error {
	allowedRoles, known := lookupAllowedRoles(moduleName, action)
	if !known {
		return &UnknownActionError{ModuleName: moduleName, Action: action}
	}
	if !containsRole(allowedRoles, authUser.Role) {
		return &UnauthorizedRoleError{
			MemberID:   authUser.MemberID,
			Role:       authUser.Role,
			ModuleName: moduleName,
			Action:     action,
		}
	}
	return nil
}

// lookupAllowedRoles reads the policy entry for (moduleName, action). The
// second return is false when either the module is absent or the action is
// absent within a known module — both cases collapse to UnknownActionError at
// the caller.
func lookupAllowedRoles(moduleName ModuleName, action ActionName) ([]Role, bool) {
	actions, ok := policy[moduleName]
	if !ok {
		return nil, false
	}
	allowed, ok := actions[action]
	if !ok {
		return nil, false
	}
	return allowed, true
}

// containsRole reports whether r appears in roles. Kept as a tiny named helper
// so Authorize stays at a single level of abstraction.
func containsRole(roles []Role, r Role) bool {
	for _, candidate := range roles {
		if candidate == r {
			return true
		}
	}
	return false
}
