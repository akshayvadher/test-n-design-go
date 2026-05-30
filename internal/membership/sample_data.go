package membership

// NewMemberOption mutates a NewMemberDto produced by SampleNewMember.
// Options apply in the order they are passed — a later option overwrites
// an earlier one with the same target field. The functional-option pattern
// matches the project's locked convention (no overrides-struct argument).
type NewMemberOption func(*NewMemberDto)

// WithName overrides the Name.
func WithName(name string) NewMemberOption {
	return func(dto *NewMemberDto) {
		dto.Name = name
	}
}

// WithEmail overrides the Email.
func WithEmail(email string) NewMemberOption {
	return func(dto *NewMemberDto) {
		dto.Email = email
	}
}

// SampleNewMember returns a NewMemberDto defaulted to Ada Lovelace, mutated
// by the supplied options in order. The defaults match the source TS
// `sampleNewMember` exactly.
func SampleNewMember(opts ...NewMemberOption) NewMemberDto {
	dto := NewMemberDto{
		Name:  "Ada Lovelace",
		Email: "ada.lovelace@example.com",
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}

// SampleNewMemberWithEmail is a shorthand for SampleNewMember(WithEmail(email))
// so tests that only vary the email read cleanly.
func SampleNewMemberWithEmail(email string) NewMemberDto {
	return SampleNewMember(WithEmail(email))
}
