package bookcache

import (
	"context"
	"sync"
)

// InMemoryBookCacheGateway is the test substrate and the default cache
// implementation. It is safe for concurrent use.
//
// Get returns a defensive copy of the stored BookDto so callers cannot
// mutate the cached value via the returned pointer.
type InMemoryBookCacheGateway struct {
	mu      sync.RWMutex
	entries map[string]BookDto
}

// NewInMemoryBookCacheGateway constructs an empty cache.
func NewInMemoryBookCacheGateway() *InMemoryBookCacheGateway {
	return &InMemoryBookCacheGateway{
		entries: map[string]BookDto{},
	}
}

// Get returns a copy of the cached BookDto, or (nil, nil) on miss.
func (c *InMemoryBookCacheGateway) Get(_ context.Context, isbn string) (*BookDto, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[isbn]
	if !ok {
		return nil, nil
	}
	copied := cloneBook(entry)
	return &copied, nil
}

// Set stores a copy of book under the supplied ISBN.
func (c *InMemoryBookCacheGateway) Set(_ context.Context, isbn string, book BookDto) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[isbn] = cloneBook(book)
	return nil
}

// Evict removes the cached entry for the supplied ISBN. Evicting a missing
// key is a no-op and returns nil.
func (c *InMemoryBookCacheGateway) Evict(_ context.Context, isbn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, isbn)
	return nil
}

// cloneBook returns a defensive copy of book so the cache and its callers
// do not share the Authors slice backing array.
func cloneBook(book BookDto) BookDto {
	clone := book
	if book.Authors != nil {
		clone.Authors = make([]string, len(book.Authors))
		copy(clone.Authors, book.Authors)
	}
	return clone
}
