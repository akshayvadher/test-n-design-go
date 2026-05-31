package memory

import (
	"context"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
)

// Cache is the test substrate and the default bookcache.BookCacheGateway
// implementation. It is safe for concurrent use.
//
// Get returns a defensive copy of the stored BookDto so callers cannot
// mutate the cached value via the returned pointer.
type Cache struct {
	mu      sync.RWMutex
	entries map[string]bookcache.BookDto
}

// Compile-time assertion that *Cache satisfies the bookcache.BookCacheGateway
// port.
var _ bookcache.BookCacheGateway = (*Cache)(nil)

// NewCache constructs an empty in-memory cache.
func NewCache() *Cache {
	return &Cache{
		entries: map[string]bookcache.BookDto{},
	}
}

// Get returns a copy of the cached BookDto, or (nil, nil) on miss.
func (c *Cache) Get(_ context.Context, isbn string) (*bookcache.BookDto, error) {
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
func (c *Cache) Set(_ context.Context, isbn string, book bookcache.BookDto) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[isbn] = cloneBook(book)
	return nil
}

// Evict removes the cached entry for the supplied ISBN. Evicting a
// missing key is a no-op and returns nil.
func (c *Cache) Evict(_ context.Context, isbn string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, isbn)
	return nil
}

// cloneBook returns a defensive copy of book so the cache and its
// callers do not share the Authors slice backing array.
func cloneBook(book bookcache.BookDto) bookcache.BookDto {
	clone := book
	if book.Authors != nil {
		clone.Authors = make([]string, len(book.Authors))
		copy(clone.Authors, book.Authors)
	}
	return clone
}
