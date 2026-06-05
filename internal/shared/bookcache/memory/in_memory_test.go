// in_memory_test.go covers Slice 1's minimum-viable contract for
// InMemoryBookCacheGateway: Set-then-Get round-trips the value, Get of an
// unset key returns (nil, nil) per the port docstring, Evict removes an
// existing key and is a no-op on a missing key, and Get returns a defensive
// slice copy so caller mutations cannot reach back into stored state.
//
// Slice 2's facade-level catalog spec drives the cache in anger via the
// catalog.Facade. This file pins the bare contract the facade depends on.
package memory

import (
	"context"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
)

// -----------------------------------------------------------------------------
// Set + Get — happy round-trip.
// -----------------------------------------------------------------------------

func TestInMemoryBookCacheGateway_GetAfterSet_ReturnsValue(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	want := bookcache.BookDto{
		BookId:  "b-1",
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    "978-0135957059",
	}
	if err := cache.Set(ctx, "978-0135957059", want); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	got, err := cache.Get(ctx, "978-0135957059")
	if err != nil {
		t.Fatalf("Get: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("Get: got nil, want %+v", want)
	}
	if got.BookId != want.BookId || got.Title != want.Title || got.Isbn != want.Isbn {
		t.Errorf("Get: got %+v, want %+v", *got, want)
	}
	if !equalStrings(got.Authors, want.Authors) {
		t.Errorf("Authors: got %v, want %v", got.Authors, want.Authors)
	}
}

// -----------------------------------------------------------------------------
// Get on an unset key — (nil, nil), the port-docstring "miss" signal the
// catalog facade depends on to fall through to the repository.
// -----------------------------------------------------------------------------

func TestInMemoryBookCacheGateway_Get_UnsetKeyReturnsNilNil(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()

	got, err := cache.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Get: got %+v, want nil (port contract: unset key => nil, nil)", got)
	}
}

// -----------------------------------------------------------------------------
// Evict — removes an existing entry; Get afterwards is a miss.
// -----------------------------------------------------------------------------

func TestInMemoryBookCacheGateway_Evict_RemovesExistingEntry(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	if err := cache.Set(ctx, "isbn-1", bookcache.BookDto{BookId: "b-1", Isbn: "isbn-1"}); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	if err := cache.Evict(ctx, "isbn-1"); err != nil {
		t.Fatalf("Evict: got error %v, want nil", err)
	}

	got, err := cache.Get(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("Get after Evict: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("Get after Evict: got %+v, want nil", got)
	}
}

// -----------------------------------------------------------------------------
// Evict on a missing key — no-op, returns nil per the port docstring.
// -----------------------------------------------------------------------------

func TestInMemoryBookCacheGateway_Evict_MissingKeyIsNoOp(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()

	if err := cache.Evict(ctx, "never-set"); err != nil {
		t.Errorf("Evict: got error %v, want nil (no-op on missing key)", err)
	}
}

// -----------------------------------------------------------------------------
// Get returns a defensive slice copy — the catalog facade reads the cached
// bookcache.BookDto and passes Authors through to HTTP response mapping; caller-side
// mutation of the returned slice must not corrupt the cache.
// -----------------------------------------------------------------------------

func TestInMemoryBookCacheGateway_Get_ReturnsDefensiveSliceCopy(t *testing.T) {
	cache := NewCache()
	ctx := context.Background()
	if err := cache.Set(ctx, "isbn-1", bookcache.BookDto{
		BookId:  "b-1",
		Title:   "T",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    "isbn-1",
	}); err != nil {
		t.Fatalf("Set: got error %v, want nil", err)
	}

	first, err := cache.Get(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("Get: got error %v, want nil", err)
	}
	if first == nil || len(first.Authors) != 2 {
		t.Fatalf("Get: unexpected initial value %+v", first)
	}
	first.Authors[0] = "MUTATED"

	second, err := cache.Get(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("Get (second): got error %v, want nil", err)
	}
	if second.Authors[0] != "Andrew Hunt" {
		t.Errorf("Authors[0]: got %q, want %q (mutation leaked into cache)", second.Authors[0], "Andrew Hunt")
	}
}

// -----------------------------------------------------------------------------
// Small assertion helper — local to the package, stdlib only.
// -----------------------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
