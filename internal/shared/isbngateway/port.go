// Package isbngateway is the shared outbound port for ISBN metadata lookup.
// Modules that need to enrich a partial book record from an external catalog
// depend on the IsbnLookupGateway interface; the composition root wires the
// concrete implementation.
//
// Phase 2 ships:
//
//   - InMemoryIsbnLookupGateway — the test substrate and the production
//     default until an external lookup is configured.
//   - OpenLibraryIsbnLookupGateway — a placeholder stub that returns an
//     "not implemented" error so the implementation slot is reserved
//     without committing to a wire shape. Phase 5+ supplies the real body.
//
// The package depends on the standard library only. It has no awareness of
// catalog or any other module — BookMetadata is the boundary shape.
package isbngateway

import "context"

// BookMetadata is the value the gateway returns for a found ISBN: the
// authoritative title and authors as the external source records them. The
// gateway never invents ISBNs — the ISBN is the input.
type BookMetadata struct {
	Title   string
	Authors []string
}

// IsbnLookupGateway looks up book metadata for a given ISBN. The interface is
// the only seam business modules depend on; the constructor wires the
// concrete implementation.
//
// FindByIsbn returns (nil, nil) when the gateway has no record for the
// supplied ISBN — that is the canonical "not found" signal and matches the
// repository convention in the rest of the codebase. A non-nil error is an
// infrastructure failure (network, decode, etc.); the caller decides whether
// to fail open or fail closed.
type IsbnLookupGateway interface {
	FindByIsbn(ctx context.Context, isbn string) (*BookMetadata, error)
}
