package memory

import (
	"io"
	"log/slog"

	"github.com/google/uuid"

	"github.com/akshayvadher/test-n-design-go/internal/accesscontrol"
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
	"github.com/akshayvadher/test-n-design-go/internal/shared/bookcache"
	"github.com/akshayvadher/test-n-design-go/internal/shared/isbngateway"
)

// Overrides is the test-substitution extension point for the catalog
// Facade. Every field is optional — a zero value means "use the default."
// Tests construct it with only the fields they need to swap (typically
// NewID for deterministic ids and IsbnLookupGateway when exercising
// enrichment).
type Overrides struct {
	Repository        catalog.Repository
	NewID             func() string
	IsbnLookupGateway isbngateway.IsbnLookupGateway
	BookCacheGateway  bookcache.BookCacheGateway
	AccessControl     *accesscontrol.Facade
	Logger            *slog.Logger
}

// NewFacadeWithOverrides constructs a catalog.Facade applying the
// supplied Overrides on top of the in-memory defaults. The defaults
// wire a fresh in-memory Repository, a UUID-string id generator, fresh
// in-memory ISBN and book-cache gateways, a default accesscontrol
// Facade, and a silent slog logger. Tests reuse this constructor so each
// test gets a clean substrate without restating the wiring.
func NewFacadeWithOverrides(o Overrides) *catalog.Facade {
	repository := o.Repository
	if repository == nil {
		repository = NewRepository()
	}
	newID := o.NewID
	if newID == nil {
		newID = uuid.NewString
	}
	gateway := o.IsbnLookupGateway
	if gateway == nil {
		gateway = isbngateway.NewInMemoryIsbnLookupGateway()
	}
	cache := o.BookCacheGateway
	if cache == nil {
		cache = bookcache.NewInMemoryBookCacheGateway()
	}
	authz := o.AccessControl
	if authz == nil {
		authz = accesscontrol.NewFacade()
	}
	logger := o.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return catalog.NewFacade(repository, newID, gateway, cache, authz, logger)
}
