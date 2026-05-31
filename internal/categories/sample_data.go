package categories

import "time"

// CategoryOption mutates a CategoryDto produced by SampleNewCategory.
// Options apply in the order they are passed — a later option
// overwrites an earlier one with the same target field. The
// functional-option pattern matches the project's locked convention
// (no overrides-struct argument).
type CategoryOption func(*CategoryDto)

// WithCategoryId overrides the CategoryId.
func WithCategoryId(id CategoryId) CategoryOption {
	return func(dto *CategoryDto) {
		dto.CategoryId = id
	}
}

// WithCategoryName overrides the Name.
func WithCategoryName(name string) CategoryOption {
	return func(dto *CategoryDto) {
		dto.Name = name
	}
}

// WithCategoryCreatedAt overrides the CreatedAt timestamp.
func WithCategoryCreatedAt(t time.Time) CategoryOption {
	return func(dto *CategoryDto) {
		dto.CreatedAt = t
	}
}

// defaultSampleCreatedAt is the deterministic timestamp every
// SampleNewCategory carries unless overridden. Matches the TS source's
// `new Date('2030-01-15T00:00:00.000Z')` fixture exactly.
var defaultSampleCreatedAt = time.Date(2030, 1, 15, 0, 0, 0, 0, time.UTC)

// SampleNewCategory returns a CategoryDto defaulted to a "Fiction"
// fixture, mutated by the supplied options in order. The defaults
// match the source TS `sampleCategory` exactly (including the
// placeholder UUID).
func SampleNewCategory(opts ...CategoryOption) CategoryDto {
	dto := CategoryDto{
		CategoryId: "00000000-0000-0000-0000-000000000001",
		Name:       "Fiction",
		CreatedAt:  defaultSampleCreatedAt,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}
