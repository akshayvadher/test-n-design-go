package isbngateway

import (
	"context"
	"sync"
)

// InMemoryIsbnLookupGateway is the test substrate and the Phase 2 production
// default for IsbnLookupGateway. It stores seeded BookMetadata in a map and
// is safe for concurrent use.
//
// Seed populates an entry; subsequent FindByIsbn returns a copy of that
// entry. Callers cannot mutate the gateway's stored state via the returned
// pointer — Authors is defensively copied on each read.
type InMemoryIsbnLookupGateway struct {
	mu      sync.RWMutex
	entries map[string]BookMetadata
}

// NewInMemoryIsbnLookupGateway constructs an empty gateway.
func NewInMemoryIsbnLookupGateway() *InMemoryIsbnLookupGateway {
	return &InMemoryIsbnLookupGateway{
		entries: map[string]BookMetadata{},
	}
}

// Seed stores metadata under the supplied ISBN. The Authors slice is copied
// so later caller mutations do not leak into the gateway.
func (g *InMemoryIsbnLookupGateway) Seed(isbn string, metadata BookMetadata) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.entries[isbn] = BookMetadata{
		Title:   metadata.Title,
		Authors: copyAuthors(metadata.Authors),
	}
}

// FindByIsbn returns a defensive copy of the seeded metadata, or (nil, nil)
// when the ISBN is unknown. The error is reserved for infrastructure
// failures, which an in-memory implementation cannot encounter.
func (g *InMemoryIsbnLookupGateway) FindByIsbn(_ context.Context, isbn string) (*BookMetadata, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	entry, ok := g.entries[isbn]
	if !ok {
		return nil, nil
	}
	return &BookMetadata{
		Title:   entry.Title,
		Authors: copyAuthors(entry.Authors),
	}, nil
}

// copyAuthors returns a defensive copy of the supplied slice so the gateway
// and its callers do not share the underlying array.
func copyAuthors(authors []string) []string {
	if authors == nil {
		return nil
	}
	copied := make([]string, len(authors))
	copy(copied, authors)
	return copied
}
