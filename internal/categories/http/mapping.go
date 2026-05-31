package http

import (
	"github.com/akshayvadher/test-n-design-go/internal/categories"
)

// toCategoryResponse translates a categories.CategoryDto into the
// outbound HTTP DTO. The string cast unwraps the CategoryId named type
// so JSON encoding sees a plain string under the wire key `id`.
func toCategoryResponse(category categories.CategoryDto) CategoryResponse {
	return CategoryResponse{
		Id:        string(category.CategoryId),
		Name:      category.Name,
		CreatedAt: category.CreatedAt,
	}
}

// toCategoryResponseSlice projects a slice of categories.CategoryDto
// into a slice of wire CategoryResponse values, preserving order.
func toCategoryResponseSlice(in []categories.CategoryDto) []CategoryResponse {
	out := make([]CategoryResponse, 0, len(in))
	for _, category := range in {
		out = append(out, toCategoryResponse(category))
	}
	return out
}
