package categories

import "context"

// CategoryRepository is the persistence port the categories facade
// depends on. The in-memory adapter and the bun-backed Postgres adapter
// both satisfy it.
//
// Save translates a name-collision (case-insensitive) into a
// *DuplicateCategoryError. FindById returns (nil, nil) on miss — the
// facade is responsible for translating that into
// *CategoryNotFoundError. A non-nil error indicates infrastructure
// failure (decode, transport, …) and is propagated unchanged.
//
// FindByNamePrefix must:
//   - match case-insensitively (lowercase prefix vs lowercase name);
//   - return results sorted by name ascending (case-insensitive);
//   - cap the result at MaxPrefixResults rows;
//   - return [] (not an error) when nothing matches.
type CategoryRepository interface {
	Save(ctx context.Context, category CategoryDto) error
	FindById(ctx context.Context, id CategoryId) (*CategoryDto, error)
	FindByNamePrefix(ctx context.Context, prefix string) ([]CategoryDto, error)
}

// MaxPrefixResults caps the number of rows FindByNamePrefix returns.
// Matches the TS source's MAX_PREFIX_RESULTS constant 1:1.
const MaxPrefixResults = 100
