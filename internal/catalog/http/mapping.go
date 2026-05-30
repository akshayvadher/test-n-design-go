package http

import (
	"github.com/akshayvadher/test-n-design-go/internal/catalog"
)

// fromAddBookRequest translates the inbound HTTP DTO into the catalog
// domain DTO. The translation is field-for-field; validation lives inside
// the facade (it parses the merged-with-gateway dto). The Isbn string is
// cast to catalog.Isbn unchanged.
func fromAddBookRequest(req AddBookRequest) catalog.NewBookDto {
	return catalog.NewBookDto{
		Title:   req.Title,
		Authors: req.Authors,
		Isbn:    catalog.Isbn(req.Isbn),
	}
}

// fromUpdateBookRequest translates the inbound patch HTTP DTO into the
// catalog domain DTO. Pointer fields pass through directly so the schema
// parser sees the same "field absent" vs "field present" distinction the
// JSON decoder produced.
func fromUpdateBookRequest(req UpdateBookRequest) catalog.UpdateBookDto {
	return catalog.UpdateBookDto{
		Title:   req.Title,
		Authors: req.Authors,
	}
}

// toBookResponse translates a catalog.BookDto into the outbound HTTP DTO.
// String casts unwrap the BookId / Isbn named types so JSON encoding sees
// plain strings.
func toBookResponse(book catalog.BookDto) BookResponse {
	return BookResponse{
		BookId:  string(book.BookId),
		Title:   book.Title,
		Authors: book.Authors,
		Isbn:    string(book.Isbn),
	}
}

// toCopyResponse translates a catalog.CopyDto into the outbound HTTP DTO.
func toCopyResponse(copy catalog.CopyDto) CopyResponse {
	return CopyResponse{
		CopyId:    string(copy.CopyId),
		BookId:    string(copy.BookId),
		Condition: string(copy.Condition),
		Status:    string(copy.Status),
	}
}
