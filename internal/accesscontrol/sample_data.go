package accesscontrol

// AuthUserOption mutates an AuthUser produced by SampleAuthUser or
// SampleStaffAuthUser. Options apply in the order they are passed — a later
// option overwrites an earlier one with the same target field. Callers who
// want a specific role start from the matching builder (SampleAuthUser for
// MEMBER, SampleStaffAuthUser for STAFF) rather than overriding the role.
type AuthUserOption func(*AuthUser)

// WithMemberID overrides the MemberID on the AuthUser under construction.
func WithMemberID(id string) AuthUserOption {
	return func(u *AuthUser) {
		u.MemberID = id
	}
}

// WithRole overrides the Role on the AuthUser under construction. Documented
// but rarely needed: prefer the matching Sample*AuthUser builder so the
// intent reads at the call site.
func WithRole(r Role) AuthUserOption {
	return func(u *AuthUser) {
		u.Role = r
	}
}

// SampleAuthUser returns a MEMBER AuthUser with a placeholder MemberID,
// mutated by the supplied options in order.
//
// This is the only place in the module — outside tests — that constructs an
// AuthUser literal. Business code receives AuthUser values from upstream
// (HTTP middleware / facade callers) and never builds them inline.
func SampleAuthUser(opts ...AuthUserOption) AuthUser {
	user := AuthUser{
		MemberID: "member-placeholder-id",
		Role:     RoleMember,
	}
	for _, opt := range opts {
		opt(&user)
	}
	return user
}

// SampleStaffAuthUser returns a STAFF AuthUser with a placeholder MemberID,
// mutated by the supplied options in order.
func SampleStaffAuthUser(opts ...AuthUserOption) AuthUser {
	user := AuthUser{
		MemberID: "staff-placeholder-id",
		Role:     RoleStaff,
	}
	for _, opt := range opts {
		opt(&user)
	}
	return user
}
