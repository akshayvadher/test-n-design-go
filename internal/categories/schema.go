package categories

import "strings"

// maxCategoryNameLength caps the length of a category name. The TS
// source's zod schema does not enforce a hard upper bound; the Go port
// adds 100 chars as a defensive default so a runaway name does not blow
// past Postgres TEXT-column expectations or render badly in clients.
const maxCategoryNameLength = 100

// ParseNewCategory trims name, rejects blanks, and rejects names whose
// length exceeds maxCategoryNameLength. On success it returns the
// trimmed name. On any failure it returns the first validator complaint
// wrapped in *InvalidCategoryError so the HTTP layer can translate it
// into a 400 via the domain-error registry.
func ParseNewCategory(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", &InvalidCategoryError{Reason: "name is required"}
	}
	if len(trimmed) > maxCategoryNameLength {
		return "", &InvalidCategoryError{Reason: "name too long"}
	}
	return trimmed, nil
}

// ParseStartsWith trims raw and rejects blanks. On success it returns
// the trimmed prefix. On failure it returns
// *InvalidCategoriesQueryError so the HTTP layer can translate it into
// a 400.
func ParseStartsWith(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", &InvalidCategoriesQueryError{Reason: "startsWith is required"}
	}
	return trimmed, nil
}
