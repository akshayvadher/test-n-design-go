// Package http is the catalog module's HTTP edge. Every type defined here is
// a wire shape — a JSON request body or response envelope — and exists only
// to be encoded/decoded across the network boundary. None of these types
// leak out of this package: the handlers translate inbound DTOs into
// catalog.NewBookDto / catalog.UpdateBookDto / catalog.NewCopyDto via the
// (also-unexported) mapping helpers, and translate outbound catalog.BookDto
// / catalog.CopyDto into response shapes before writing the body.
//
// The JSON tags match the source TypeScript API contract byte-for-byte
// (camelCase, not snake_case) so client integrations work unchanged across
// the port.
package http

// AddBookRequest is the inbound body for POST /books. Title and Authors are
// plain (not pointer) types because the facade's enrichment step uses a
// blank-string / empty-slice check to decide whether the ISBN gateway
// should fill them — a "field absent" vs "field present with empty value"
// distinction is unnecessary here.
type AddBookRequest struct {
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// UpdateBookRequest is the inbound body for PATCH /books/{bookId}. Pointer
// fields disambiguate "field absent" (nil) from "field present with empty
// value" so the schema parser can treat each case correctly. Isbn is
// intentionally absent: a book's ISBN is immutable and the handler relies
// on DisallowUnknownFields to reject any inbound body that carries it.
type UpdateBookRequest struct {
	Title   *string   `json:"title,omitempty"`
	Authors *[]string `json:"authors,omitempty"`
}

// BookResponse is the outbound body for every endpoint that returns a single
// book (or one element of the ListBooks array). Field order and JSON keys
// match the source TS API contract verbatim.
type BookResponse struct {
	BookId  string   `json:"bookId"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// NewCopyRequest is the inbound body for POST /books/{bookId}/copies. BookId
// is NOT in the body — the handler reads it from the URL parameter.
type NewCopyRequest struct {
	Condition string `json:"condition"`
}

// CopyResponse is the outbound body for every endpoint that returns a copy.
type CopyResponse struct {
	CopyId    string `json:"copyId"`
	BookId    string `json:"bookId"`
	Condition string `json:"condition"`
	Status    string `json:"status"`
}
