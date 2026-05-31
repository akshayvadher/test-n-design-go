// Package http is the categories module's HTTP edge. Every type
// defined here is a wire shape — a JSON request body or response
// envelope — and exists only to be encoded/decoded across the network
// boundary. None of these types leak out of this package: the handlers
// translate inbound DTOs into categories.CategoryDto via the
// unexported mapping helpers, and translate outbound categories.CategoryDto
// into response shapes before writing the body.
//
// The JSON tags match the source TypeScript API contract byte-for-byte
// (camelCase, the wire key `id` rather than `categoryId`) so client
// integrations work unchanged across the port.
package http

import "time"

// CreateCategoryRequest is the inbound body for POST /categories.
type CreateCategoryRequest struct {
	Name string `json:"name"`
}

// CategoryResponse is the outbound body for every endpoint that
// returns a category. The wire key is `id`, NOT `categoryId`, to
// preserve byte-for-byte compatibility with the TS source.
type CategoryResponse struct {
	Id        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
}
