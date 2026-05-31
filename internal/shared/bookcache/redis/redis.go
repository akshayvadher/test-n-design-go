package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	upstreamredis "github.com/redis/go-redis/v9"

	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
)

// DefaultTTL is the cache entry lifetime used when callers do not pass
// an explicit duration to NewCache. Five minutes matches the TS source's
// default and is short enough that a stale book record self-heals
// without manual eviction.
const DefaultTTL = 5 * time.Minute

// keyPrefix is the exact key namespace from the TS source
// (`redis-book-cache-gateway.ts` line 32). Keeping the prefix in
// source-fidelity lock-step with the TS port lets the Go service and a
// hypothetical TS service share the same Redis instance without
// stomping each other's entries.
const keyPrefix = "catalog:book:isbn:"

// Cache is the Redis-backed implementation of
// bookcache.BookCacheGateway. The composition root constructs it when
// LIBRARY_REDIS_URL is set; the in-memory adapter remains the unit-test
// substrate.
//
// The struct holds a *redis.Client owned by the composition root.
// Closing the client is the caller's responsibility (chained into
// app.Wired.Close).
type Cache struct {
	client *upstreamredis.Client
	ttl    time.Duration
	logger *slog.Logger
}

// Compile-time assertion that *Cache satisfies the
// bookcache.BookCacheGateway port.
var _ bookcache.BookCacheGateway = (*Cache)(nil)

// NewCache constructs a Redis-backed cache. ttl is the per-entry
// expiration applied to every Set; a non-positive ttl falls back to
// DefaultTTL so callers cannot accidentally write entries that never
// expire. logger is required — pass a discard logger if you do not want
// Redis errors logged.
func NewCache(client *upstreamredis.Client, ttl time.Duration, logger *slog.Logger) *Cache {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	return &Cache{client: client, ttl: ttl, logger: logger}
}

// Get returns the cached BookDto for isbn, or (nil, nil) on cache miss.
// A non-miss Redis error propagates wrapped — callers (the catalog
// facade) treat it as a fatal cache failure rather than a silent
// fallthrough.
func (c *Cache) Get(ctx context.Context, isbn string) (*bookcache.BookDto, error) {
	key := redisKey(isbn)
	raw, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, upstreamredis.Nil) {
		return nil, nil
	}
	if err != nil {
		c.logger.Error("bookcache redis get", slog.String("op", "get"), slog.String("key", key), slog.String("error", err.Error()))
		return nil, fmt.Errorf("bookcache redis get %s: %w", key, err)
	}
	var wire wireBook
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("bookcache redis decode %s: %w", key, err)
	}
	book := wire.toBookDto()
	return &book, nil
}

// Set serializes book to JSON and writes it under the namespaced key
// with the gateway's configured TTL. Errors are logged at error level
// and returned wrapped so the caller (the catalog facade) can choose to
// surface or swallow.
func (c *Cache) Set(ctx context.Context, isbn string, book bookcache.BookDto) error {
	key := redisKey(isbn)
	payload, err := json.Marshal(wireBookFrom(book))
	if err != nil {
		return fmt.Errorf("bookcache redis encode %s: %w", key, err)
	}
	if err := c.client.Set(ctx, key, payload, c.ttl).Err(); err != nil {
		c.logger.Error("bookcache redis set", slog.String("op", "set"), slog.String("key", key), slog.String("error", err.Error()))
		return fmt.Errorf("bookcache redis set %s: %w", key, err)
	}
	return nil
}

// Evict deletes the cached entry for isbn. Deleting a missing key is a
// no-op (Redis DEL returns 0; not an error). Infrastructure errors are
// logged and returned wrapped.
func (c *Cache) Evict(ctx context.Context, isbn string) error {
	key := redisKey(isbn)
	if err := c.client.Del(ctx, key).Err(); err != nil {
		c.logger.Error("bookcache redis evict", slog.String("op", "evict"), slog.String("key", key), slog.String("error", err.Error()))
		return fmt.Errorf("bookcache redis evict %s: %w", key, err)
	}
	return nil
}

// redisKey builds the namespaced cache key for an ISBN. Source-fidelity
// with the TS port: `catalog:book:isbn:<isbn>`.
func redisKey(isbn string) string {
	return keyPrefix + isbn
}

// wireBook is the on-the-wire JSON shape stored in Redis. Holding it as
// a separate struct (rather than tagging BookDto directly) keeps the
// public BookDto free of JSON struct tags and lets the wire format
// evolve independently of the Go field names.
type wireBook struct {
	BookId  string   `json:"book_id"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// wireBookFrom translates a BookDto into the wire shape.
func wireBookFrom(book bookcache.BookDto) wireBook {
	return wireBook{
		BookId:  book.BookId,
		Title:   book.Title,
		Authors: book.Authors,
		Isbn:    book.Isbn,
	}
}

// toBookDto translates the wire shape back into a BookDto.
func (r wireBook) toBookDto() bookcache.BookDto {
	return bookcache.BookDto{
		BookId:  r.BookId,
		Title:   r.Title,
		Authors: r.Authors,
		Isbn:    r.Isbn,
	}
}
