package catalog

// NewBookOption mutates a NewBookDto produced by SampleNewBook. Options
// apply in the order they are passed — a later option overwrites an
// earlier one with the same target field. The functional-option pattern
// matches the project's locked convention (no overrides-struct argument).
type NewBookOption func(*NewBookDto)

// WithTitle overrides the Title.
func WithTitle(title string) NewBookOption {
	return func(dto *NewBookDto) {
		dto.Title = title
	}
}

// WithAuthors overrides the Authors slice.
func WithAuthors(authors []string) NewBookOption {
	return func(dto *NewBookDto) {
		dto.Authors = authors
	}
}

// WithIsbn overrides the Isbn.
func WithIsbn(isbn Isbn) NewBookOption {
	return func(dto *NewBookDto) {
		dto.Isbn = isbn
	}
}

// SampleNewBook returns a NewBookDto defaulted to The Pragmatic Programmer,
// mutated by the supplied options in order. The defaults match the source
// TS `sampleNewBook` exactly.
func SampleNewBook(opts ...NewBookOption) NewBookDto {
	dto := NewBookDto{
		Title:   "The Pragmatic Programmer",
		Authors: []string{"Andrew Hunt", "David Thomas"},
		Isbn:    "978-0135957059",
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}

// SampleNewBookWithIsbn is a shorthand for SampleNewBook(WithIsbn(isbn)) so
// tests that only vary the ISBN read cleanly.
func SampleNewBookWithIsbn(isbn Isbn) NewBookDto {
	return SampleNewBook(WithIsbn(isbn))
}

// UpdateBookOption mutates an UpdateBookDto produced by SampleUpdateBook.
type UpdateBookOption func(*UpdateBookDto)

// WithUpdateTitle sets the Title pointer to a fresh string.
func WithUpdateTitle(title string) UpdateBookOption {
	return func(dto *UpdateBookDto) {
		copied := title
		dto.Title = &copied
	}
}

// WithUpdateAuthors sets the Authors pointer to a fresh slice.
func WithUpdateAuthors(authors []string) UpdateBookOption {
	return func(dto *UpdateBookDto) {
		copied := append([]string(nil), authors...)
		dto.Authors = &copied
	}
}

// WithUpdateTitleNil explicitly clears the Title pointer. Useful for tests
// that exercise the "field absent" path.
func WithUpdateTitleNil() UpdateBookOption {
	return func(dto *UpdateBookDto) {
		dto.Title = nil
	}
}

// WithUpdateAuthorsNil explicitly clears the Authors pointer.
func WithUpdateAuthorsNil() UpdateBookOption {
	return func(dto *UpdateBookDto) {
		dto.Authors = nil
	}
}

// SampleUpdateBook returns an UpdateBookDto defaulted to a non-nil title
// and non-nil authors patch, mutated by the supplied options in order.
func SampleUpdateBook(opts ...UpdateBookOption) UpdateBookDto {
	defaultTitle := "Updated Title"
	defaultAuthors := []string{"Updated Author"}
	dto := UpdateBookDto{
		Title:   &defaultTitle,
		Authors: &defaultAuthors,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}

// NewCopyOption mutates a NewCopyDto produced by SampleNewCopy.
type NewCopyOption func(*NewCopyDto)

// WithBookId overrides the BookId.
func WithBookId(bookId BookId) NewCopyOption {
	return func(dto *NewCopyDto) {
		dto.BookId = bookId
	}
}

// WithCondition overrides the Condition.
func WithCondition(condition CopyCondition) NewCopyOption {
	return func(dto *NewCopyDto) {
		dto.Condition = condition
	}
}

// SampleNewCopy returns a NewCopyDto defaulted to a placeholder BookId and
// GOOD condition, mutated by the supplied options in order.
func SampleNewCopy(opts ...NewCopyOption) NewCopyDto {
	dto := NewCopyDto{
		BookId:    "book-placeholder-id",
		Condition: CopyConditionGood,
	}
	for _, opt := range opts {
		opt(&dto)
	}
	return dto
}
