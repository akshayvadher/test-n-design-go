package catalog

import (
	"regexp"
	"strings"
)

// isbn10Pattern matches an ISBN-10 after hyphens and spaces have been
// stripped: nine decimal digits followed by a tenth character that is a
// digit or the literal capital X. Matches the source TS validator 1:1 —
// no checksum verification.
var isbn10Pattern = regexp.MustCompile(`^\d{9}[\dX]$`)

// isbn13Pattern matches an ISBN-13 after hyphens and spaces have been
// stripped: thirteen decimal digits.
var isbn13Pattern = regexp.MustCompile(`^\d{13}$`)

// isbnStripper removes the only two characters the source TS validator
// strips before regex-matching: hyphen and ASCII whitespace.
var isbnStripper = strings.NewReplacer("-", "", " ", "")

// ParseIsbn trims the input, rejects empty, and rejects any value whose
// hyphen/space-stripped form does not match ISBN-10 or ISBN-13. On success
// it returns the trimmed (but otherwise unchanged) value as an Isbn — the
// hyphens stay; only the validation pass strips them.
func ParseIsbn(raw string) (Isbn, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", &InvalidBookError{Reason: "isbn is required"}
	}
	normalized := isbnStripper.Replace(trimmed)
	if !isbn10Pattern.MatchString(normalized) && !isbn13Pattern.MatchString(normalized) {
		return "", &InvalidBookError{Reason: "isbn format is invalid: " + trimmed}
	}
	return Isbn(trimmed), nil
}

// ParseNewBook trims the title and each author, filters out blank authors,
// and validates that title, at least one author and a well-formed ISBN are
// all present. On any failure it returns the first validator complaint
// wrapped in InvalidBookError. On success it returns the trimmed and
// filtered dto.
func ParseNewBook(dto NewBookDto) (NewBookDto, error) {
	title := strings.TrimSpace(dto.Title)
	if title == "" {
		return NewBookDto{}, &InvalidBookError{Reason: "title is required"}
	}
	authors := trimAndFilterAuthors(dto.Authors)
	if len(authors) == 0 {
		return NewBookDto{}, &InvalidBookError{Reason: "at least one author is required"}
	}
	isbn, err := ParseIsbn(string(dto.Isbn))
	if err != nil {
		return NewBookDto{}, err
	}
	return NewBookDto{Title: title, Authors: authors, Isbn: isbn}, nil
}

// ParseUpdateBook validates a patch dto. At least one field must be present
// (non-nil); when Title is non-nil it must be non-blank after trimming;
// when Authors is non-nil the trimmed-and-filtered slice must be non-empty.
// On success it returns a copy of the dto with the trimmed/filtered values
// reattached via fresh pointers.
func ParseUpdateBook(dto UpdateBookDto) (UpdateBookDto, error) {
	if dto.Title == nil && dto.Authors == nil {
		return UpdateBookDto{}, &InvalidBookError{Reason: "at least one of title or authors must be provided"}
	}
	parsed := UpdateBookDto{}
	if dto.Title != nil {
		trimmed := strings.TrimSpace(*dto.Title)
		if trimmed == "" {
			return UpdateBookDto{}, &InvalidBookError{Reason: "title is required"}
		}
		parsed.Title = &trimmed
	}
	if dto.Authors != nil {
		filtered := trimAndFilterAuthors(*dto.Authors)
		if len(filtered) == 0 {
			return UpdateBookDto{}, &InvalidBookError{Reason: "at least one author is required"}
		}
		parsed.Authors = &filtered
	}
	return parsed, nil
}

// ParseNewCopy validates that Condition is one of the four documented
// values. BookId is not validated here — the facade dereferences the
// book via repository.FindBookById and surfaces BookNotFoundError if the
// id is unknown.
func ParseNewCopy(dto NewCopyDto) (NewCopyDto, error) {
	if !isValidCopyCondition(dto.Condition) {
		return NewCopyDto{}, &InvalidCopyError{Reason: "condition must be one of NEW, GOOD, FAIR, POOR"}
	}
	return dto, nil
}

// trimAndFilterAuthors trims each author and drops blank entries. Returns a
// fresh slice (the input is never mutated).
func trimAndFilterAuthors(authors []string) []string {
	filtered := make([]string, 0, len(authors))
	for _, author := range authors {
		trimmed := strings.TrimSpace(author)
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}

// isValidCopyCondition reports whether c is one of the four constants
// exported in types.go.
func isValidCopyCondition(c CopyCondition) bool {
	switch c {
	case CopyConditionNew, CopyConditionGood, CopyConditionFair, CopyConditionPoor:
		return true
	}
	return false
}
