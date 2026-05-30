package isbngateway

import (
	"context"
	"errors"
)

// OpenLibraryIsbnLookupGateway is the placeholder for the real external
// lookup. Phase 2 ships only the in-memory implementation; this type exists
// so the implementation slot is visible and the wire shape is locked. The
// real HTTP call to openlibrary.org lands in Phase 5+ — until then, every
// method returns ErrNotImplemented so a misconfigured composition root
// fails loudly instead of silently returning empty data.
type OpenLibraryIsbnLookupGateway struct{}

// NewOpenLibraryIsbnLookupGateway constructs the placeholder.
func NewOpenLibraryIsbnLookupGateway() *OpenLibraryIsbnLookupGateway {
	return &OpenLibraryIsbnLookupGateway{}
}

// ErrNotImplemented is returned by every placeholder method. The variable is
// exported so callers can distinguish the placeholder failure from a real
// infrastructure error via errors.Is.
var ErrNotImplemented = errors.New("isbngateway: OpenLibraryIsbnLookupGateway is not implemented yet")

// FindByIsbn is unimplemented in Phase 2.
func (g *OpenLibraryIsbnLookupGateway) FindByIsbn(_ context.Context, _ string) (*BookMetadata, error) {
	return nil, ErrNotImplemented
}
