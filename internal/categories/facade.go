package categories

import (
	"context"
	"log/slog"
	"time"
)

// Facade is the only public surface of the categories module. Every
// business operation — creating a category, listing by prefix, finding
// by id — goes through one of its exported methods. Unexported fields
// keep its collaborators encapsulated; the composition root wires them
// via NewFacade and tests substitute them via NewFacadeWithOverrides.
type Facade struct {
	repository CategoryRepository
	newID      func() string
	clock      func() time.Time
	logger     *slog.Logger
}

// NewFacade wires the Facade with explicit dependencies. The composition
// root passes the concrete implementations; tests use
// NewFacadeWithOverrides which fills the same arguments from an
// Overrides struct with in-memory defaults.
func NewFacade(repository CategoryRepository, newID func() string, clock func() time.Time, logger *slog.Logger) *Facade {
	return &Facade{
		repository: repository,
		newID:      newID,
		clock:      clock,
		logger:     logger,
	}
}

// CreateCategory validates name, mints a fresh CategoryId, stamps
// CreatedAt from the facade's clock, persists the category (the
// repository may surface *DuplicateCategoryError on a name collision),
// and returns the saved value.
func (f *Facade) CreateCategory(ctx context.Context, name string) (CategoryDto, error) {
	parsedName, err := ParseNewCategory(name)
	if err != nil {
		return CategoryDto{}, err
	}
	category := CategoryDto{
		CategoryId: CategoryId(f.newID()),
		Name:       parsedName,
		CreatedAt:  f.clock(),
	}
	if err := f.repository.Save(ctx, category); err != nil {
		return CategoryDto{}, err
	}
	return category, nil
}

// FindCategoryById loads the category by id. Unknown id returns
// *CategoryNotFoundError so the HTTP layer can translate it into 404.
func (f *Facade) FindCategoryById(ctx context.Context, id CategoryId) (CategoryDto, error) {
	category, err := f.repository.FindById(ctx, id)
	if err != nil {
		return CategoryDto{}, err
	}
	if category == nil {
		return CategoryDto{}, &CategoryNotFoundError{Identifier: string(id)}
	}
	return *category, nil
}

// ListByPrefix validates prefix via ParseStartsWith, then delegates to
// the repository's FindByNamePrefix. Returns *InvalidCategoriesQueryError
// when prefix is blank so the HTTP layer can return 400.
func (f *Facade) ListByPrefix(ctx context.Context, prefix string) ([]CategoryDto, error) {
	parsedPrefix, err := ParseStartsWith(prefix)
	if err != nil {
		return nil, err
	}
	return f.repository.FindByNamePrefix(ctx, parsedPrefix)
}
