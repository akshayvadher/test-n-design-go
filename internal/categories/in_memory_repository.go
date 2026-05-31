package categories

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// InMemoryCategoryRepository is the in-memory CategoryRepository
// implementation. It is safe for concurrent use. Categories are stored
// by CategoryId; the name uniqueness check is a linear scan because the
// test substrate never holds enough rows for the scan to matter
// (matches the TS source's choice exactly).
type InMemoryCategoryRepository struct {
	mu             sync.RWMutex
	categoriesById map[CategoryId]CategoryDto
}

// NewInMemoryCategoryRepository constructs an empty
// InMemoryCategoryRepository.
func NewInMemoryCategoryRepository() *InMemoryCategoryRepository {
	return &InMemoryCategoryRepository{
		categoriesById: map[CategoryId]CategoryDto{},
	}
}

// Save persists category. Before writing, it scans the existing
// categories for one with a different id but the same Name
// (case-insensitive) and returns *DuplicateCategoryError when found.
// On no collision the category is stored by value.
func (r *InMemoryCategoryRepository) Save(_ context.Context, category CategoryDto) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	normalized := strings.ToLower(category.Name)
	for _, existing := range r.categoriesById {
		if existing.CategoryId != category.CategoryId && strings.ToLower(existing.Name) == normalized {
			return &DuplicateCategoryError{Name: category.Name}
		}
	}
	r.categoriesById[category.CategoryId] = category
	return nil
}

// FindById returns the stored category by value, or (nil, nil) on miss.
func (r *InMemoryCategoryRepository) FindById(_ context.Context, id CategoryId) (*CategoryDto, error) {
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
// (case-insensitively), capped at MaxPrefixResults.
func (r *InMemoryCategoryRepository) FindByNamePrefix(_ context.Context, prefix string) ([]CategoryDto, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	lowerPrefix := strings.ToLower(prefix)
	matches := make([]CategoryDto, 0)
	for _, category := range r.categoriesById {
		if strings.HasPrefix(strings.ToLower(category.Name), lowerPrefix) {
			matches = append(matches, category)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
	})
	if len(matches) > MaxPrefixResults {
		matches = matches[:MaxPrefixResults]
	}
	return matches, nil
}
