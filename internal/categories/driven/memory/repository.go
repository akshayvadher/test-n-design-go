package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/akshayvadher/test-n-design-go/internal/categories"
)

// Repository is the in-memory categories.CategoryRepository
// implementation. It is safe for concurrent use. Categories are stored
// by CategoryId; the name uniqueness check is a linear scan because the
// test substrate never holds enough rows for the scan to matter
// (matches the TS source's choice exactly).
type Repository struct {
	mu             sync.RWMutex
	categoriesById map[categories.CategoryId]categories.CategoryDto
}

// Compile-time assertion that *Repository satisfies the categories
// driven port. If a method signature drifts, the assertion fails before
// any test runs.
var _ categories.CategoryRepository = (*Repository)(nil)

// NewRepository constructs an empty in-memory Repository.
func NewRepository() *Repository {
	return &Repository{
		categoriesById: map[categories.CategoryId]categories.CategoryDto{},
	}
}

// Save persists category. Before writing, it scans the existing
// categories for one with a different id but the same Name
// (case-insensitive) and returns *categories.DuplicateCategoryError
// when found. On no collision the category is stored by value.
func (r *Repository) Save(_ context.Context, category categories.CategoryDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	normalized := strings.ToLower(category.Name)
	for _, existing := range r.categoriesById {
		if existing.CategoryId != category.CategoryId && strings.ToLower(existing.Name) == normalized {
			return &categories.DuplicateCategoryError{Name: category.Name}
		}
	}
	r.categoriesById[category.CategoryId] = category
	return nil
}

// FindById returns the stored category by value, or (nil, nil) on miss.
func (r *Repository) FindById(_ context.Context, id categories.CategoryId) (*categories.CategoryDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	category, ok := r.categoriesById[id]
	if !ok {
		return nil, nil
	}
	return &category, nil
}

// FindByNamePrefix returns every stored category whose Name starts with
// prefix (case-insensitively), sorted ascending by Name
// (case-insensitively), capped at categories.MaxPrefixResults.
func (r *Repository) FindByNamePrefix(_ context.Context, prefix string) ([]categories.CategoryDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lowerPrefix := strings.ToLower(prefix)
	matches := make([]categories.CategoryDto, 0)
	for _, category := range r.categoriesById {
		if strings.HasPrefix(strings.ToLower(category.Name), lowerPrefix) {
			matches = append(matches, category)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
	})
	if len(matches) > categories.MaxPrefixResults {
		matches = matches[:categories.MaxPrefixResults]
	}
	return matches, nil
}
