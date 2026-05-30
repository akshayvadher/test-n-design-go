package accesscontrol

// Overrides is the test-substitution extension point for the accesscontrol
// module. It is intentionally empty in Phase 1 — the Facade has no
// collaborators worth swapping yet — but exists so callers and tests can wire
// against a stable type today and add fields later without changing the
// constructor signature.
type Overrides struct{}

// NewFacadeWithOverrides constructs a Facade applying the supplied Overrides.
// In Phase 1 the parameter is ignored; the function exists so the composition
// root and tests have a single, named extension point that matches the
// pattern other modules follow.
func NewFacadeWithOverrides(_ Overrides) *Facade {
	return NewFacade()
}
