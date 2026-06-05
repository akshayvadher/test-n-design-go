package external

import (
	"context"
	"errors"

	"github.com/akshayvadher/test-n-design-go/internal/shared/isbngateway"
)

// Gateway is the placeholder for the real external lookup. Phase 2
// ships only the in-memory implementation; this type exists so the
// implementation slot is visible and the wire shape is locked. The real
// HTTP call to openlibrary.org lands in Phase 5+ — until then, every
// method returns ErrNotImplemented so a misconfigured composition root
// fails loudly instead of silently returning empty data.
type Gateway struct{}

// Compile-time assertion that *Gateway satisfies the
// isbngateway.IsbnLookupGateway port.
var _ isbngateway.IsbnLookupGateway = (*Gateway)(nil)

// NewGateway constructs the placeholder.
func NewGateway() *Gateway {
	return &Gateway{}
}

// ErrNotImplemented is returned by every placeholder method. The
// variable is exported so callers can distinguish the placeholder
// failure from a real infrastructure error via errors.Is.
var ErrNotImplemented = errors.New("isbngateway: external Gateway is not implemented yet")

// FindByIsbn is unimplemented in Phase 2.
func (g *Gateway) FindByIsbn(_ context.Context, _ string) (*isbngateway.BookMetadata, error) {
	return nil, ErrNotImplemented
}
