// in_memory_test.go covers Slice 1's minimum-viable contract for
// InMemoryIsbnLookupGateway: Seed populates an entry, FindByIsbn returns it,
// FindByIsbn on an unseeded ISBN returns (nil, nil) per the port docstring,
// and the returned slice is a defensive copy so caller mutations cannot leak
// into stored state.
//
// Slice 2's facade-level catalog spec will drive the gateway in anger via
// the catalog.Facade. This file pins the bare contract the catalog facade
// will assume.
package memory

import (
	"context"
	"testing"

	"github.com/akshayvadher/test-n-design-go/internal/shared/isbngateway"
)

// -----------------------------------------------------------------------------
// FindByIsbn on a seeded ISBN returns a copy of the stored metadata.
// -----------------------------------------------------------------------------

func TestInMemoryIsbnLookupGateway_Lookup_SeededIsbnReturnsMetadata(t *testing.T) {
	gateway := NewGateway()
	ctx := context.Background()
	want := isbngateway.BookMetadata{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
	}
	gateway.Seed("978-0135957059", want)

	got, err := gateway.FindByIsbn(ctx, "978-0135957059")
	if err != nil {
		t.Fatalf("FindByIsbn: got error %v, want nil", err)
	}
	if got == nil {
		t.Fatalf("FindByIsbn: got nil, want %+v", want)
	}
	if got.Title != want.Title {
		t.Errorf("Title: got %q, want %q", got.Title, want.Title)
	}
	if !equalStrings(got.Authors, want.Authors) {
		t.Errorf("Authors: got %v, want %v", got.Authors, want.Authors)
	}
}

// -----------------------------------------------------------------------------
// FindByIsbn on an unseeded ISBN returns (nil, nil) — the port-docstring
// "not found" signal the catalog facade enrichment step depends on.
// -----------------------------------------------------------------------------

func TestInMemoryIsbnLookupGateway_Lookup_UnknownIsbnReturnsNilNil(t *testing.T) {
	gateway := NewGateway()
	ctx := context.Background()

	got, err := gateway.FindByIsbn(ctx, "not-seeded")
	if err != nil {
		t.Fatalf("FindByIsbn: got error %v, want nil", err)
	}
	if got != nil {
		t.Errorf("FindByIsbn: got %+v, want nil (port contract: unknown ISBN => nil, nil)", got)
	}
}

// -----------------------------------------------------------------------------
// FindByIsbn returns a defensive slice copy — the port docstring promises
// callers cannot mutate gateway state via the returned pointer.
// -----------------------------------------------------------------------------

func TestInMemoryIsbnLookupGateway_FindByIsbn_ReturnsDefensiveSliceCopy(t *testing.T) {
	gateway := NewGateway()
	ctx := context.Background()
	gateway.Seed("isbn-1", isbngateway.BookMetadata{
		Title:   "T",
		Authors: []string{"Andrew Hunt", "David Thomas"},
	})

	first, err := gateway.FindByIsbn(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("FindByIsbn: got error %v, want nil", err)
	}
	if first == nil || len(first.Authors) != 2 {
		t.Fatalf("FindByIsbn: unexpected initial value %+v", first)
	}
	first.Authors[0] = "MUTATED"

	second, err := gateway.FindByIsbn(ctx, "isbn-1")
	if err != nil {
		t.Fatalf("FindByIsbn (second): got error %v, want nil", err)
	}
	if second.Authors[0] != "Andrew Hunt" {
		t.Errorf("Authors[0]: got %q, want %q (mutation leaked into gateway state)", second.Authors[0], "Andrew Hunt")
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
