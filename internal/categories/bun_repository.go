package categories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"
)

// uniqueViolationSQLState is the Postgres SQLSTATE for a unique
// constraint violation. The bun driver surfaces it as a substring of
// the error message ("SQLSTATE=23505"); matching on the substring
// avoids depending on the driver's typed error.
const uniqueViolationSQLState = "23505"

// isInvalidUUIDSyntax detects the Postgres "invalid input syntax for
// type uuid" error so caller-supplied garbage IDs collapse to (nil, nil)
// — same shape as a real not-found. Without this, GET /categories/garbage
// would 500 instead of the spec-mandated 404.
func isInvalidUUIDSyntax(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE=22P02") || strings.Contains(msg, "invalid input syntax for type uuid")
}

// isUniqueViolation detects the Postgres unique-constraint failure on
// the categories_name_unique index. The driver surfaces SQLSTATE=23505
// as a substring of the error message.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SQLSTATE="+uniqueViolationSQLState)
}

// CategoryRow is the bun-mapped persistent shape of a category. JSON
// tags are intentionally absent — this struct never crosses the HTTP
// boundary; the HTTP DTOs in internal/categories/http own that.
//
// Column names match migrations/0005_categories.sql verbatim.
type CategoryRow struct {
	bun.BaseModel `bun:"table:categories"`

	CategoryId CategoryId `bun:"category_id,pk"`
	Name       string     `bun:"name,notnull"`
	CreatedAt  time.Time  `bun:"created_at,notnull"`
}

// BunCategoryRepository is the Postgres-backed CategoryRepository
// implementation. Writes run directly against the base *bun.DB —
// categories does NOT integrate with TransactionalContext (no
// cross-aggregate writes, no events).
type BunCategoryRepository struct {
	db *bun.DB
}

// Compile-time assertion that BunCategoryRepository satisfies
// CategoryRepository. If a method signature drifts, the assertion fails
// before any test runs.
var _ CategoryRepository = (*BunCategoryRepository)(nil)

// NewBunCategoryRepository constructs a BunCategoryRepository bound to
// db. The caller owns the *bun.DB lifecycle.
func NewBunCategoryRepository(db *bun.DB) *BunCategoryRepository {
	return &BunCategoryRepository{db: db}
}

// Save inserts category. On the Postgres unique-constraint violation
// raised by categories_name_unique (a UNIQUE INDEX on LOWER(name)) the
// error is translated to *DuplicateCategoryError so the HTTP layer
// can return 409. Other errors are wrapped and propagated.
func (r *BunCategoryRepository) Save(ctx context.Context, category CategoryDto) error {
	row := toCategoryRow(category)
	_, err := r.db.NewInsert().Model(&row).Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return &DuplicateCategoryError{Name: category.Name}
		}
		return fmt.Errorf("save category %q: %w", category.CategoryId, err)
	}
	return nil
}

// FindById returns the category by primary key, or (nil, nil) on miss.
// A 22P02 (invalid_text_representation) error from a non-UUID id also
// collapses to (nil, nil) so the HTTP layer returns 404 for garbage ids.
func (r *BunCategoryRepository) FindById(ctx context.Context, id CategoryId) (*CategoryDto, error) {
	var row CategoryRow
	err := r.db.NewSelect().Model(&row).Where("category_id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) || isInvalidUUIDSyntax(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find category by id %q: %w", id, err)
	}
	category := toCategoryDto(row)
	return &category, nil
}

// FindByNamePrefix returns every category whose name starts with
// prefix (case-insensitively), sorted ascending by lower(name), capped
// at MaxPrefixResults.
func (r *BunCategoryRepository) FindByNamePrefix(ctx context.Context, prefix string) ([]CategoryDto, error) {
	var rows []CategoryRow
	err := r.db.NewSelect().
		Model(&rows).
		Where("LOWER(name) LIKE LOWER(?) || '%'", prefix).
		OrderExpr("LOWER(name) ASC").
		Limit(MaxPrefixResults).
		Scan(ctx)
	if err != nil {
		return nil, fmt.Errorf("find categories by name prefix %q: %w", prefix, err)
	}
	out := make([]CategoryDto, 0, len(rows))
	for _, row := range rows {
		out = append(out, toCategoryDto(row))
	}
	return out, nil
}

// toCategoryRow projects a domain CategoryDto into the bun row.
func toCategoryRow(category CategoryDto) CategoryRow {
	return CategoryRow{
		CategoryId: category.CategoryId,
		Name:       category.Name,
		CreatedAt:  category.CreatedAt,
	}
}

// toCategoryDto projects a bun row back into a domain CategoryDto.
func toCategoryDto(row CategoryRow) CategoryDto {
	return CategoryDto{
		CategoryId: row.CategoryId,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
	}
}
