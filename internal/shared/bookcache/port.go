// Package bookcache is the shared outbound port for caching book records by
// ISBN. The catalog module reads through the cache on FindBook and writes
// through on UpdateBook / DeleteBook; no other module touches it in Phase 2.
//
// Phase 2 ships:
//
//   - InMemoryBookCacheGateway — the test substrate and the default
//     implementation until Redis is wired.
//
// The Redis-backed implementation lands in a later Phase-2 slice (Slices
// 6–7 per the phase-2 spec). Both implementations satisfy BookCacheGateway.
//
// The package owns its own BookDto type rather than importing
// internal/catalog. This breaks what would otherwise be an import cycle
// (catalog depends on bookcache for the cache port; bookcache would depend
// on catalog for the cached value type) and keeps bookcache a leaf package
// the way internal/shared/events is. The catalog facade converts between
// catalog.BookDto and bookcache.BookDto inline at the cache call sites.
//
// The package depends on the standard library only.
package bookcache

import "context"

// BookDto is the boundary shape the cache stores. It mirrors the catalog
// module's BookDto field-for-field — the catalog facade is responsible for
// translating between the two types at every cache call. The mirror keeps
// bookcache a leaf package with no dependency on business modules.
type BookDto struct {
	BookId  string
	Title   string
	Authors []string
	Isbn    string
}

// BookCacheGateway is the read-through / write-through cache the catalog
// facade consults. Get returns (nil, nil) on cache miss — that is the
// canonical "not present" signal and matches the repository convention. Set
// and Evict return only infrastructure errors; a successful no-op (e.g.
// evicting a missing key) returns nil.
type BookCacheGateway interface {
	Get(ctx context.Context, isbn string) (*BookDto, error)
	Set(ctx context.Context, isbn string, book BookDto) error
	Evict(ctx context.Context, isbn string) error
}
