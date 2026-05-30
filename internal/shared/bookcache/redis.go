package bookcache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// DefaultRedisTTL is the cache entry lifetime used when callers do not pass an
// explicit duration to NewRedisBookCacheGateway. Five minutes matches the TS
// source's default and is short enough that a stale book record self-heals
// without manual eviction.
const DefaultRedisTTL = 5 * time.Minute

// redisKeyPrefix is the exact key namespace from the TS source
// (`redis-book-cache-gateway.ts` line 32). Keeping the prefix in source-fidelity
// lock-step with the TS port lets the Go service and a hypothetical TS service
// share the same Redis instance without stomping each other's entries.
const redisKeyPrefix = "catalog:book:isbn:"

// RedisBookCacheGateway is the Redis-backed implementation of
// BookCacheGateway. The composition root constructs it when LIBRARY_REDIS_URL
// is set; the InMemoryBookCacheGateway remains the unit-test substrate.
//
// The struct holds a *redis.Client owned by the composition root. Closing the
// client is the caller's responsibility (chained into app.Wired.Close).
type RedisBookCacheGateway struct {
	client *redis.Client
	ttl    time.Duration
	logger *slog.Logger
}

// Compile-time assertion that *RedisBookCacheGateway satisfies the port.
var _ BookCacheGateway = (*RedisBookCacheGateway)(nil)

// NewRedisBookCacheGateway constructs a Redis-backed cache. ttl is the
// per-entry expiration applied to every Set; a non-positive ttl falls back to
// DefaultRedisTTL so callers cannot accidentally write entries that never
// expire. logger is required — pass a discard logger if you do not want
// Redis errors logged.
func NewRedisBookCacheGateway(client *redis.Client, ttl time.Duration, logger *slog.Logger) *RedisBookCacheGateway {
	if ttl <= 0 {
		ttl = DefaultRedisTTL
	}
	return &RedisBookCacheGateway{client: client, ttl: ttl, logger: logger}
}

// Get returns the cached BookDto for isbn, or (nil, nil) on cache miss. A
// non-miss Redis error propagates wrapped — callers (the catalog facade)
// treat it as a fatal cache failure rather than a silent fallthrough.
func (c *RedisBookCacheGateway) Get(ctx context.Context, isbn string) (*BookDto, error) {
	key := redisKey(isbn)
	raw, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		c.logger.Error("bookcache redis get", slog.String("op", "get"), slog.String("key", key), slog.String("error", err.Error()))
		return nil, fmt.Errorf("bookcache redis get %s: %w", key, err)
	}
	var wire redisBook
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("bookcache redis decode %s: %w", key, err)
	}
	book := wire.toBookDto()
	return &book, nil
}

// Set serializes book to JSON and writes it under the namespaced key with the
// gateway's configured TTL. Errors are logged at error level and returned
// wrapped so the caller (the catalog facade) can choose to surface or swallow.
func (c *RedisBookCacheGateway) Set(ctx context.Context, isbn string, book BookDto) error {
	key := redisKey(isbn)
	payload, err := json.Marshal(redisBookFrom(book))
	if err != nil {
		return fmt.Errorf("bookcache redis encode %s: %w", key, err)
	}
	if err := c.client.Set(ctx, key, payload, c.ttl).Err(); err != nil {
		c.logger.Error("bookcache redis set", slog.String("op", "set"), slog.String("key", key), slog.String("error", err.Error()))
		return fmt.Errorf("bookcache redis set %s: %w", key, err)
	}
	return nil
}

// Evict deletes the cached entry for isbn. Deleting a missing key is a no-op
// (Redis DEL returns 0; not an error). Infrastructure errors are logged and
// returned wrapped.
func (c *RedisBookCacheGateway) Evict(ctx context.Context, isbn string) error {
	key := redisKey(isbn)
	if err := c.client.Del(ctx, key).Err(); err != nil {
		c.logger.Error("bookcache redis evict", slog.String("op", "evict"), slog.String("key", key), slog.String("error", err.Error()))
		return fmt.Errorf("bookcache redis evict %s: %w", key, err)
	}
	return nil
}

// redisKey builds the namespaced cache key for an ISBN. Source-fidelity with
// the TS port: `catalog:book:isbn:<isbn>`.
func redisKey(isbn string) string {
	return redisKeyPrefix + isbn
}

// redisBook is the on-the-wire JSON shape stored in Redis. Holding it as a
// separate struct (rather than tagging BookDto directly) keeps the public
// BookDto free of JSON struct tags and lets the wire format evolve
// independently of the Go field names.
type redisBook struct {
	BookId  string   `json:"book_id"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Isbn    string   `json:"isbn"`
}

// redisBookFrom translates a BookDto into the wire shape.
func redisBookFrom(book BookDto) redisBook {
	return redisBook{
		BookId:  book.BookId,
		Title:   book.Title,
		Authors: book.Authors,
		Isbn:    book.Isbn,
	}
}

// toBookDto translates the wire shape back into a BookDto.
func (r redisBook) toBookDto() BookDto {
	return BookDto{
		BookId:  r.BookId,
		Title:   r.Title,
		Authors: r.Authors,
		Isbn:    r.Isbn,
	}
}
